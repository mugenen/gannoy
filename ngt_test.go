package gannoy

import (
	"io/ioutil"
	"math/rand"
	"path/filepath"
	"runtime"
	"sync"
	"testing"

	ngt "github.com/monochromegane/go-ngt"
)

func TestCreateGraphAndTree(t *testing.T) {
	property, _ := ngt.NewNGTProperty(1)
	defer property.Free()

	index, err := CreateGraphAndTree(tempDatabaseDir("db"), property)
	if err != nil {
		t.Errorf("CreateGraphAndTree should not return error, but return %v", err)
	}
	defer index.Drop()
}

func TestNewNGTIndex(t *testing.T) {
	index := testCreateGraphAndTree("db", 1)
	defer index.Drop()
	index.Save()

	_, err := NewNGTIndex(index.database, 1)
	if err != nil {
		t.Errorf("NewNGTIndex should not return error, but return %v", err)
	}
}

func TestAddItemAndRemoveItem(t *testing.T) {
	index := testCreateGraphAndTree("db", 1)
	defer index.Drop()

	key := 100
	err := index.AddItem(key, []float64{1.0})
	if err != nil {
		t.Errorf("NGTIndex.AddItem should not return error, but return %v", err)
	}

	keys, err := index.GetNnsByKey(uint(key), 1, 0.1)
	if keys[0] != key {
		t.Errorf("NGTIndex.AddItem should register object, but dose not register.")
	}

	err = index.RemoveItem(key)
	if err != nil {
		t.Errorf("NGTIndex.RemoveItem should not return error, but return %v", err)
	}

	keys, err = index.GetNnsByKey(uint(key), 1, 0.1)
	if err == nil {
		t.Errorf("NGTIndex.RemoveItem should delete object, but dose not delete.")
	}
}

func TestUpdateItemWhenNotExist(t *testing.T) {
	index := testCreateGraphAndTree("db", 1)
	defer index.Drop()

	key := 100
	err := index.UpdateItem(key, []float64{1.0})
	if err != nil {
		t.Errorf("NGTIndex.UpdateItem should not return error, but return %v", err)
	}

	keys, err := index.GetNnsByKey(uint(key), 1, 0.1)
	if keys[0] != key {
		t.Errorf("NGTIndex.AddItem should register object, but dose not register.")
	}
}

func TestUpdateItemWhenExist(t *testing.T) {
	index := testCreateGraphAndTree("db", 1)
	defer index.Drop()

	key := 100
	err := index.UpdateItem(key, []float64{1.0})
	if err != nil {
		t.Errorf("NGTIndex.UpdateItem should not return error, but return %v", err)
	}

	keys, err := index.GetNnsByKey(uint(key), 1, 0.1)
	if keys[0] != key {
		t.Errorf("NGTIndex.AddItem should register object, but dose not register.")
	}

	err = index.AddItem(key+1, []float64{0.1}) // Avoid stopping the NGT when there are 0 objects.
	if err != nil {
		t.Errorf("NGTIndex.AddItem should not return error, but return %v", err)
	}
	err = index.UpdateItem(key, []float64{0.2})
	if err != nil {
		t.Errorf("NGTIndex.UpdateItem should not return error, but return %v", err)
	}

	keys, err = index.GetNnsByKey(uint(key), 10, 0.1)
	if len(keys) > 3 {
		t.Errorf("NGTIndex.AddItem should update object, but inserted new one.")
	}
	if keys[0] != key {
		t.Errorf("NGTIndex.AddItem should register object, but dose not register.")
	}
}

func TestUpdateItemConcurrently(t *testing.T) {
	objectNum := 3
	dim := 2048
	index := testCreateGraphAndTree("db", dim)
	// defer index.Drop()

	// Insert 1,000 objects
	for i := 0; i < objectNum; i++ {
		vec := make([]float64, dim)
		for j := 0; j < dim; j++ {
			vec[j] = rand.Float64()
		}
		_, err := index.addItemWithoutCreateIndex(i, vec)
		if err != nil {
			t.Errorf("NGTIndex.UpdateItem should not return error, but return %v", err)
		}
	}
	err := index.index.CreateIndex(runtime.NumCPU())
	if err != nil {
		t.Errorf("NGTIndex.CreateItem should not return error, but return %v", err)
	}
	index.Save()
	file := index.database
	index, _ = NewNGTIndex(file, runtime.NumCPU()) // Avoid warining GraphAndTreeIndex::insert empty
	defer index.Drop()

	// UpdateItem concurrently
	vec := make([]float64, dim)
	for j := 0; j < dim; j++ {
		vec[j] = rand.Float64()
	}

	var wg sync.WaitGroup
	wg.Add(objectNum)
	worker := func(inputChan chan int) {
		for key := range inputChan {
			index.UpdateItem(key, vec)
			wg.Done()
		}
	}
	inputChan := make(chan int, objectNum)
	for i := 0; i < runtime.NumCPU(); i++ {
		go worker(inputChan)
	}

	for i := 0; i < objectNum; i++ {
		inputChan <- i
	}
	wg.Wait()

	key := 0
	keys, err := index.GetNnsByKey(uint(key), objectNum+10, 0.1)
	if len(keys) > objectNum {
		t.Errorf("NGTIndex.AddItem should update object, but inserted new one.")
	}
}

func testCreateGraphAndTree(database string, dim int) NGTIndex {
	property, _ := ngt.NewNGTProperty(dim)
	defer property.Free()
	index, _ := CreateGraphAndTree(tempDatabaseDir(database), property)
	return index
}

func tempDatabaseDir(database string) string {
	dir, _ := ioutil.TempDir("", "gannoy-test")
	return filepath.Join(dir, database)
}
