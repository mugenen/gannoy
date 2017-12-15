package gannoy

import (
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"path"
	"path/filepath"
	"time"

	ngt "github.com/monochromegane/go-ngt"
)

type NGTIndex struct {
	database string
	index    ngt.NGTIndex
	pair     Pair
	thread   int
	timeout  time.Duration
	bin      BinLog
	ctx      context.Context
	cancel   context.CancelFunc
	exitCh   chan struct{}
}

func CreateGraphAndTree(database string, property ngt.NGTProperty) (NGTIndex, error) {
	index, err := ngt.CreateGraphAndTree(database, property)
	if err != nil {
		return NGTIndex{}, err
	}
	pair, err := newPair(database + ".map")
	if err != nil {
		return NGTIndex{}, err
	}
	bin := NewBinLog(database + ".bin")
	err = bin.Open()
	if err != nil {
		return NGTIndex{}, err
	}
	idx := NGTIndex{
		database: database,
		index:    index,
		pair:     pair,
		bin:      bin,
	}
	return idx, nil
}

func NewNGTIndex(database string, thread int, timeout time.Duration) (NGTIndex, error) {
	index, err := ngt.OpenIndex(database)
	if err != nil {
		return NGTIndex{}, err
	}
	pair, err := newPair(database + ".map")
	if err != nil {
		return NGTIndex{}, err
	}
	bin := NewBinLog(database + ".bin")
	err = bin.Open()
	if err != nil {
		return NGTIndex{}, err
	}
	return NGTIndex{
		database: database,
		index:    index,
		pair:     pair,
		thread:   thread,
		timeout:  timeout,
		bin:      bin,
	}, nil
}

func (idx *NGTIndex) WaitApplyFromBinLog(d time.Duration, resultCh chan ApplicationResult) {
	idx.ctx, idx.cancel = context.WithCancel(context.Background())
	idx.exitCh = make(chan struct{})

	go func() {
		defer func() {
			idx.exitCh <- struct{}{}
		}()

		t := time.NewTicker(d)
		defer t.Stop()

		childCtx, cancel := context.WithCancel(idx.ctx)
		defer cancel()

	loop:
		for {
			select {
			case <-t.C:
				result := idx.ApplyFromBinLog(childCtx)
				resultCh <- result
				if result.Err == nil {
					break loop
				}
			case <-idx.ctx.Done():
				break loop
			}
		}
	}()
}

func (idx NGTIndex) String() string {
	return filepath.Base(idx.database)
}

type searchResult struct {
	ids []int
	err error
}

func (idx *NGTIndex) SearchItem(key uint, limit int, epsilon float32) ([]int, error) {
	resultCh := make(chan searchResult, 1)
	go func() {
		ids, err := idx.GetNnsByKey(key, limit, epsilon)
		resultCh <- searchResult{ids: ids, err: err}
		close(resultCh)
	}()
	result := idx.searchWithTimeout(resultCh)
	return result.ids, result.err
}

func (idx *NGTIndex) GetNnsByKey(key uint, n int, epsilon float32) ([]int, error) {
	if id, ok := idx.pair.idFromKey(key); !ok {
		return nil, fmt.Errorf("Not found")
	} else {
		v, err := idx.getItem(id.(uint))
		if err != nil {
			return nil, err
		}
		ids, err := idx.GetAllNns(v, n, epsilon)
		if err != nil {
			return nil, err
		}
		keys := make([]int, len(ids))
		for i, id_ := range ids {
			if key, ok := idx.pair.keyFromId(uint(id_)); ok {
				keys[i] = int(key.(uint))
			}
		}
		return keys, nil
	}
}

func (idx *NGTIndex) GetAllNns(v []float64, n int, epsilon float32) ([]int, error) {
	results, err := idx.index.Search(v, n, epsilon)
	ids := make([]int, len(results))
	for i, result := range results {
		ids[i] = int(result.Id)
	}
	return ids, newNGTSearchErrorFrom(err)
}

func (idx *NGTIndex) UpdateBinLog(key, action int, features []byte) error {
	return idx.bin.Add(key, action, features)
}

type Feature struct {
	W []float64 `json:"features"`
}

type ApplicationResult struct {
	Key     string
	Current string
	Base    string
	DB      string
	Map     string
	Index   *NGTIndex
	Err     error
}

func (idx *NGTIndex) ApplyFromBinLog(ctx context.Context) ApplicationResult {
	resultCh := make(chan ApplicationResult)

	go func() {
		resultCh <- idx.applyFromBinLog()
	}()

	for {
		select {
		case result := <-resultCh:
			return result
		case <-ctx.Done():
			return ApplicationResult{Key: idx.String(), Err: fmt.Errorf("Canceled")}
		}
	}
}

func (idx *NGTIndex) ApplyToDB(result ApplicationResult) error {
	defer os.RemoveAll(result.Base)
	defer idx.bin.Clear(result.Current)

	err := os.Rename(result.Map, idx.pair.file)
	if err != nil {
		return err
	}
	files, err := ioutil.ReadDir(result.DB)
	if err != nil {
		return err
	}
	for _, f := range files {
		err = os.Rename(filepath.Join(result.DB, f.Name()), filepath.Join(idx.database, f.Name()))
		if err != nil {
			return err
		}
	}
	return nil
}

func (idx *NGTIndex) applyFromBinLog() ApplicationResult {
	tmp, err := ioutil.TempDir("", "gannoy")
	if err != nil {
		return ApplicationResult{Key: idx.String(), Err: err}
	}

	rmTmp := true
	defer func() {
		if rmTmp {
			os.RemoveAll(tmp)
		}
	}()

	// Get current time
	current := time.Now().Format("2006-01-02 15:04:05")

	// Select from binlog where current time
	cnt, err := idx.bin.Count(current)
	if err != nil {
		return ApplicationResult{Key: idx.String(), Err: err}
	} else if cnt == 0 {
		return ApplicationResult{Key: idx.String(), Err: TargetNotExistError{}}
	}

	rows, err := idx.bin.Get(current)
	if err != nil {
		return ApplicationResult{Key: idx.String(), Err: err}
	}
	defer rows.Close()

	// Open as new NGTIndex
	closeIdx := true
	index, err := NewNGTIndex(idx.database, idx.thread, idx.timeout)
	if err != nil {
		return ApplicationResult{Key: idx.String(), Err: err}
	}
	defer func() {
		if closeIdx {
			index.Close()
		}
	}()

	// Apply binlog
	for rows.Next() {
		var key int
		var action int
		var features []byte

		err := rows.Scan(&key, &action, &features)
		if err != nil {
			return ApplicationResult{Key: idx.String(), Err: err}
		}
		switch action {
		case DELETE:
			if id, ok := index.pair.idFromKey(uint(key)); ok {
				err := index.index.RemoveIndex(id.(uint))
				if err != nil {
					return ApplicationResult{Key: idx.String(), Err: err}
				}
				index.pair.removeByKey(uint(key))
			}
		case UPDATE:
			var f Feature
			err = json.Unmarshal(features, &f)
			if err != nil {
				return ApplicationResult{Key: idx.String(), Err: err}
			}
			newId, err := index.index.InsertIndex(f.W)
			if err != nil {
				return ApplicationResult{Key: idx.String(), Err: err}
			}
			index.pair.addPair(uint(key), newId)
		}
	}

	tmpmap := filepath.Join(tmp, path.Base(idx.pair.file))
	err = index.pair.saveAs(tmpmap)
	if err != nil {
		return ApplicationResult{Key: idx.String(), Err: err}
	}
	err = index.index.CreateIndex(idx.thread)
	if err != nil {
		return ApplicationResult{Key: idx.String(), Err: err}
	}
	tmpdb := filepath.Join(tmp, path.Base(idx.database))
	err = index.index.SaveIndex(tmpdb)
	if err != nil {
		return ApplicationResult{Key: idx.String(), Err: err}
	}

	rmTmp = false
	closeIdx = false
	return ApplicationResult{
		Key:     idx.String(),
		Current: current,
		Base:    tmp,
		DB:      tmpdb,
		Map:     tmpmap,
		Index:   &index,
	}
}

func (idx *NGTIndex) Save() error {
	err := idx.pair.save()
	if err != nil {
		return err
	}
	return idx.index.SaveIndex(idx.database)
}

func (idx *NGTIndex) searchWithTimeout(resultCh chan searchResult) searchResult {
	ctx, cancel := context.WithTimeout(context.Background(), idx.timeout)
	defer cancel()
	select {
	case result := <-resultCh:
		return result
	case <-ctx.Done():
		return searchResult{err: newTimeoutErrorFrom(ctx.Err())}
	}
}

func (idx *NGTIndex) getItem(id uint) ([]float64, error) {
	o, err := idx.index.GetObjectSpace()
	if err != nil {
		return []float64{}, err
	}

	obj, err := o.GetObjectAsFloat(int(id))
	if err != nil {
		return []float64{}, err
	}
	v := make([]float64, len(obj))
	for i, o := range obj {
		v[i] = float64(o)
	}
	return v, nil
}

func (idx *NGTIndex) existItem(id uint) bool {
	obj, err := idx.getItem(id)
	if err != nil || len(obj) == 0 {
		return false
	}
	return true
}

func (idx *NGTIndex) Close() {
	idx.index.Close()
	idx.bin.Close()
}

func (idx *NGTIndex) Cancel() {
	if idx.ctx == nil {
		return
	}
	idx.cancel()
	<-idx.exitCh
}

func (idx *NGTIndex) Drop() error {
	idx.Close()
	err := idx.pair.drop()
	if err != nil {
		return err
	}
	return os.RemoveAll(idx.database)
}
