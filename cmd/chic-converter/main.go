package main

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path"
	"strconv"

	"github.com/bcmk/siren/lib"
	"github.com/bcmk/siren/sitelib"

	_ "github.com/mattn/go-sqlite3"
)

type worker struct {
	cfg          sitelib.Config
	enabledPacks []sitelib.Pack
}

func linf(format string, v ...interface{}) { log.Printf("[INFO] "+format, v...) }
func lerr(format string, v ...interface{}) { log.Printf("[ERROR] "+format, v...) }

var checkErr = lib.CheckErr

func (s *worker) iconsCount() int {
	count := 0
	for _, i := range s.cfg.Packs {
		count += len(i.Icons)
	}
	return count
}

func copyFile(src, dst string) error {
	stat, err := os.Stat(src)
	if err != nil {
		return err
	}

	if stat.Mode().IsDir() {
		return fmt.Errorf("%s is a directory", src)
	}

	source, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { checkErr(source.Close()) }()

	destination, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer func() { checkErr(destination.Close()) }()

	_, err = io.Copy(destination, source)
	return err
}

func (s *worker) convert(icons []string) {
	iconsToConvert := map[string]bool{}
	for _, i := range icons {
		iconsToConvert[i] = true
	}
	outputFiles := path.Clean(s.cfg.Files) + ".tmp"
	for _, pack := range s.cfg.Packs {
		if !iconsToConvert["all"] && !iconsToConvert[pack.Name] {
			continue
		}
		outputDir := path.Join(outputFiles, pack.Name)
		checkErr(os.MkdirAll(outputDir, os.ModePerm))
		if pack.InputType == pack.FinalType {
			for _, icon := range pack.Icons {
				inputFile := path.Join(s.cfg.Input, pack.Name, icon.Name+"."+pack.InputType)
				outputFile := path.Join(outputDir, icon.Name+"."+pack.FinalType)
				linf("COPY %s/%s...", pack.Name, icon.Name)
				err := copyFile(inputFile, outputFile)
				if err != nil {
					lerr("ERROR")
				}
				checkErr(err)
				linf("OK")
			}
		} else if pack.InputType == "svg" && pack.FinalType == "png" {
			for _, icon := range pack.Icons {
				inputFile := path.Join(s.cfg.Input, pack.Name, icon.Name+"."+pack.InputType)
				outputFile := path.Join(outputDir, icon.Name+"."+pack.FinalType)
				linf("CONV %s/%s...", pack.Name, icon.Name)
				cmd := exec.Command("inkscape", "-h", strconv.Itoa(pack.FinalHeight), inputFile, "--export-filename", outputFile)
				var out bytes.Buffer
				cmd.Stderr = &out
				err := cmd.Run()
				_, errStat := os.Stat(outputFile)
				if err != nil || errStat != nil {
					lerr("ERROR")
					linf("RESULT\n%s", out.String())
				}
				checkErr(err)
				checkErr(errStat)
				linf("OK")
				if *verbose {
					linf("RESULT\n%s", out.String())
				}
			}
		} else {
			checkErr(fmt.Errorf("ERROR: Cannot process %s → %s", pack.InputType, pack.FinalType))
		}
		checkErr(os.RemoveAll(path.Join(s.cfg.Files, pack.Name)))
		checkErr(os.Rename(path.Join(outputFiles, pack.Name), path.Join(s.cfg.Files, pack.Name)))
	}
	checkErr(os.RemoveAll(outputFiles))
}

var verbose = flag.Bool("v", false, "verbose output")

func (s *worker) fillEnabledPacks() {
	packs := make([]sitelib.Pack, 0, len(s.cfg.Packs))
	for _, pack := range s.cfg.Packs {
		if !pack.Disable {
			packs = append(packs, pack)
		}
	}
	s.enabledPacks = packs
}

func main() {
	flag.Parse()
	if flag.NArg() < 2 {
		panic("usage: chic-converter <config> <icons...>")
	}
	w := &worker{cfg: sitelib.ReadConfig(flag.Arg(0))}
	w.fillEnabledPacks()
	linf("%d packs loaded, %d icons", len(w.cfg.Packs), w.iconsCount())
	w.convert(flag.Args()[1:])
}
