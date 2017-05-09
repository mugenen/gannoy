package main

import (
	"encoding/binary"
	"fmt"
	"os"

	flags "github.com/jessevdk/go-flags"
	"github.com/monochromegane/gannoy"
)

type Options struct {
	Dim  int    `short:"d" long:"dim" default:"2" description:"Specify size of feature dimention."`
	Tree int    `short:"t" long:"tree" default:"1" description:"Specify size of index tree."`
	K    int    `short:"K" long:"K" default:"50" description:"Specify max node size in a bucket node."`
	Path string `short:"p" long:"path" default:"." description:"Build meta file into this directory."`
	Maps string `short:"m" long:"map-path" default:"" description:"Specify key and index mapping CSV file, if exist."`
}

var opts Options

func main() {
	parser := flags.NewParser(&opts, flags.Default)
	parser.Usage = "[OPTIONS] SRC_ANNOY_FILE DEST_DATABASE_NAME"
	args, err := parser.Parse()
	if err != nil {
		os.Exit(1)
	}
	if len(args) != 2 {
		fmt.Fprintf(os.Stderr, "source annoy file and destination database name not specified.\n")
		os.Exit(1)
	}

	converter := gannoy.NewConverter(opts.Dim, opts.Tree, opts.K, binary.LittleEndian)
	err = converter.Convert(args[0], opts.Path, args[1], opts.Maps)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
}
