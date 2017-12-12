package main

import (
	"context"
	"fmt"
	"io"
	"io/ioutil"
	"math/rand"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"syscall"
	"time"

	"golang.org/x/net/netutil"

	flags "github.com/jessevdk/go-flags"
	"github.com/labstack/echo"
	"github.com/labstack/echo/middleware"
	"github.com/labstack/gommon/log"
	"github.com/lestrrat/go-server-starter/listener"
	"github.com/monochromegane/conflag"
	"github.com/monochromegane/gannoy"
	"github.com/nightlyone/lockfile"
)

type Options struct {
	DataDir           string `short:"d" long:"data-dir" default:"." description:"Specify the directory where the meta files are located."`
	LogDir            string `short:"l" long:"log-dir" default-mask:"os.Stdout" description:"Specify the log output directory."`
	LockDir           string `short:"L" long:"lock-dir" default:"." description:"Specify the lock file directory. This option is used only server-starter option."`
	WithServerStarter bool   `short:"s" long:"server-starter" description:"Use server-starter listener for server address."`
	ShutDownTimeout   int    `short:"T" long:"shutdown-timeout" default:"60" description:"Specify the number of seconds for shutdown timeout."`
	MaxConnections    int    `short:"m" long:"max-connections" default:"200" description:"Specify the number of max connections."`
	Thread            int    `short:"p" long:"thread" default-mask:"runtime.NumCPU()" description:"Specify number of thread."`
	Timeout           int    `short:"t" long:"timeout" default:"30" description:"Specify the number of seconds for timeout."`
	BinLogInterval    int    `short:"i" long:"binlog-interval" default:"300" description:"Specify the number of seconds for application binlog interval."`
	Config            string `short:"c" long:"config" default:"" description:"Configuration file path."`
	Version           bool   `short:"v" long:"version" description:"Show version"`
}

var opts Options

type Feature struct {
	W []float64 `json:"features"`
}

func main() {
	// Parse option from args and configuration file.
	conflag.LongHyphen = true
	conflag.BoolValue = false
	parser := flags.NewParser(&opts, flags.Default)
	_, err := parser.ParseArgs(os.Args[1:])
	if err != nil {
		os.Exit(1)
	}
	if opts.Version {
		fmt.Printf("%s version %s\n", parser.Name, gannoy.VERSION)
		os.Exit(0)
	}
	if opts.Config != "" {
		if args, err := conflag.ArgsFrom(opts.Config); err == nil {
			if _, err := parser.ParseArgs(args); err != nil {
				os.Exit(1)
			}
		}
	}
	_, err = parser.ParseArgs(os.Args[1:])
	if err != nil {
		os.Exit(1)
	}

	// Wait old process finishing.
	if opts.WithServerStarter {
		lock, err := initializeLock(opts.LockDir)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		defer lock.Unlock()
		for {
			if err := lock.TryLock(); err != nil {
				time.Sleep(100 * time.Millisecond)
				continue
			}
			break
		}
	}

	e := echo.New()

	// initialize log
	w, err := initializeLog(opts.LogDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	e.Logger.SetLevel(log.INFO)
	e.Logger.SetOutput(w)
	e.Use(middleware.LoggerWithConfig(middleware.LoggerConfig{Output: w}))

	// Load databases
	dirs, err := ioutil.ReadDir(opts.DataDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	thread := opts.Thread
	if thread == 0 {
		thread = runtime.NumCPU()
	}

	resultCh := make(chan gannoy.ApplicationResult)
	rand.Seed(time.Now().UnixNano())

	databases := map[string]gannoy.NGTIndex{}
	for _, dir := range dirs {
		if isDatabaseDir(dir) {
			key := dir.Name()
			r := 0
			if opts.BinLogInterval > 0 {
				r = rand.Intn(opts.BinLogInterval)
			}
			index, err := gannoy.NewNGTIndex(filepath.Join(opts.DataDir, key),
				thread,
				time.Duration(opts.Timeout)*time.Second,
				time.Duration(opts.BinLogInterval+r)*time.Second, // Shifting the execution time.
				resultCh)
			if err != nil {
				e.Logger.Warnf("Database (%s) loading failed. %s", key, err)
				continue
			}
			e.Logger.Infof("Database (%s) was successfully loaded", key)
			databases[key] = index
		}
	}

	// auto application
	exitCh := make(chan struct{})
	go func() {
		for result := range resultCh {
			key := result.Key
			if result.Err != nil {
				if _, ok := result.Err.(gannoy.TargetNotExistError); ok {
					continue
				}
				e.Logger.Warnf("Database (%s) application from binlog failed. %s", key, result.Err)
				continue
			}

			// Switch database
			current := databases[key]
			err := current.ApplyToDB(result)
			if err != nil {
				e.Logger.Warnf("Database (%s) application to DB failed. %s", key, err)
				continue
			}

			index, err := gannoy.NewNGTIndex(filepath.Join(opts.DataDir, key),
				thread,
				time.Duration(opts.Timeout)*time.Second,
				time.Duration(opts.BinLogInterval)*time.Second,
				resultCh)
			if err != nil {
				e.Logger.Warnf("Database (%s) loading failed. %s", key, err)
				continue
			}
			e.Logger.Infof("Database (%s) was successfully loaded", key)
			databases[key] = index
			current.Close()
		}
		exitCh <- struct{}{}
	}()

	// Define API
	e.GET("/search", func(c echo.Context) error {
		database := c.QueryParam("database")
		if _, ok := databases[database]; !ok {
			return c.NoContent(http.StatusNotFound)
		}
		key, err := strconv.Atoi(c.QueryParam("key"))
		if err != nil {
			key = -1
		}
		limit, err := strconv.Atoi(c.QueryParam("limit"))
		if err != nil {
			limit = 10
		}

		db := databases[database]
		r, err := db.SearchItem(uint(key), limit, 0.1)
		switch searchErr := err.(type) {
		case gannoy.NGTSearchError, gannoy.TimeoutError:
			e.Logger.Warnf("Search error (database: %s, key: %d): %s", database, key, searchErr)
		}
		if err != nil || len(r) == 0 {
			return c.NoContent(http.StatusNotFound)
		}

		return c.JSON(http.StatusOK, r)
	})

	e.PUT("/databases/:database/features/:key", func(c echo.Context) error {
		database := c.Param("database")
		if _, ok := databases[database]; !ok {
			return c.NoContent(http.StatusUnprocessableEntity)
		}
		key, err := strconv.Atoi(c.Param("key"))
		if err != nil {
			return c.NoContent(http.StatusUnprocessableEntity)
		}
		bin, err := ioutil.ReadAll(c.Request().Body)
		if err != nil {
			return c.NoContent(http.StatusUnprocessableEntity)
		}

		db := databases[database]
		err = db.UpdateBinLog(key, gannoy.UPDATE, bin)
		if err != nil {
			return c.NoContent(http.StatusUnprocessableEntity)
		}

		return c.NoContent(http.StatusOK)
	})

	e.DELETE("/databases/:database/features/:key", func(c echo.Context) error {
		database := c.Param("database")
		if _, ok := databases[database]; !ok {
			return c.NoContent(http.StatusUnprocessableEntity)
		}
		key, err := strconv.Atoi(c.Param("key"))
		if err != nil {
			return c.NoContent(http.StatusUnprocessableEntity)
		}
		db := databases[database]
		err = db.UpdateBinLog(key, gannoy.DELETE, []byte{})
		if err != nil {
			return c.NoContent(http.StatusUnprocessableEntity)
		}

		return c.NoContent(http.StatusOK)
	})

	e.GET("/health", func(c echo.Context) error {
		return c.NoContent(http.StatusOK)
	})

	e.GET("/databases", func(c echo.Context) error {
		json := make([]string, len(databases))
		i := 0
		for key, _ := range databases {
			json[i] = key
			i += 1
		}
		return c.JSON(http.StatusOK, json)
	})

	// Start server
	sig := os.Interrupt
	if opts.WithServerStarter {
		sig = syscall.SIGTERM
		listeners, err := listener.ListenAll()
		if err != nil && err != listener.ErrNoListeningTarget {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		e.Listener = netutil.LimitListener(listeners[0], opts.MaxConnections)
	} else {
		l, err := net.Listen("tcp", ":1323")
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		e.Listener = netutil.LimitListener(l, opts.MaxConnections)
	}

	go func() {
		if err := e.Start(""); err != nil {
			e.Logger.Info("Shutting down the server")
		}
	}()

	// Wait signals
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, sig, syscall.SIGUSR1)
loop:
	for {
		s := <-sigCh
		switch s {
		case syscall.SIGUSR1:
			if rw, ok := w.(*gannoy.ReopenableWriter); ok {
				err := rw.ReOpen()
				if err != nil {
					e.Logger.Warnf("ReOpen log file failed: %s", err)
				}
			}
		case sig:
			break loop
		}
	}

	// Shutdown
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(opts.ShutDownTimeout)*time.Second)
	defer cancel()
	if err := e.Shutdown(ctx); err != nil {
		e.Logger.Warn(err)
	}

	// Wait apply and switch binlog
	for _, db := range databases {
		db.Cancel()
	}
	close(resultCh)
	<-exitCh

	// Close databases
	for _, db := range databases {
		db.Close()
	}
}

func isDatabaseDir(dir os.FileInfo) bool {
	dbFiles := []string{"grp", "obj", "prf", "tre"}
	if !dir.IsDir() {
		return false
	}
	files, err := ioutil.ReadDir(filepath.Join(opts.DataDir, dir.Name()))
	if err != nil {
		return false
	}
	if len(files) != 4 {
		return false
	}
	for _, file := range files {
		if !contain(dbFiles, file.Name()) {
			return false
		}
	}
	return true
}

func contain(files []string, file string) bool {
	for _, f := range files {
		if file == f {
			return true
		}
	}
	return false
}

func initializeLock(lockDir string) (lockfile.Lockfile, error) {
	if err := os.MkdirAll(lockDir, os.ModePerm); err != nil {
		return "", err
	}
	lock := "gannoy-db.lock"
	if !filepath.IsAbs(lockDir) {
		lockDir, err := filepath.Abs(lockDir)
		if err != nil {
			return lockfile.Lockfile(""), err
		}
		return lockfile.New(filepath.Join(lockDir, lock))
	}
	return lockfile.New(filepath.Join(lockDir, lock))
}

func initializeLog(logDir string) (io.Writer, error) {
	if logDir == "" {
		return os.Stdout, nil
	}
	if err := os.MkdirAll(logDir, os.ModePerm); err != nil {
		return nil, err
	}
	return gannoy.NewReopenableWriter(filepath.Join(logDir, "gannoy-db.log"))
}
