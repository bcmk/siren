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
)

type worker struct {
	cfg   sitelib.Config
	packs []sitelib.Pack
}

func linf(format string, v ...interface{}) { log.Printf("[INFO] "+format, v...) }
func lerr(format string, v ...interface{}) { log.Printf("[ERROR] "+format, v...) }

var checkErr = lib.CheckErr

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

func makeSvgz(pack sitelib.Pack, icon sitelib.Icon, outputFile string, svgzFile string) {
	switch pack.FinalType {
	case "png":
		linf("COMPRESS TO SVGZ %s/%s...", pack.Name, icon.Name)
		cmd := exec.Command("transpeg", "-z", "-q", "80", outputFile, svgzFile)
		var transpegOut bytes.Buffer
		cmd.Stderr = &transpegOut
		err := cmd.Run()
		if err != nil {
			lerr("ERROR")
			linf("RESULT\n%s", transpegOut.String())
		}
		checkErr(err)
		linf("OK")
	case "svg":
		linf("COMPRESS TO SVGZ %s/%s...", pack.Name, icon.Name)
		cmd := exec.Command("gzip", "-nc", outputFile)
		file, err := os.Create(svgzFile)
		checkErr(err)
		defer func() { checkErr(file.Close()) }()
		var errOut bytes.Buffer
		cmd.Stderr = &errOut
		cmd.Stdout = file
		err = cmd.Run()
		if err != nil {
			lerr("ERROR")
			linf("RESULT\n%s", errOut.String())
		}
		checkErr(err)
		linf("OK")
	}
}

func makeWebp(pack sitelib.Pack, icon sitelib.Icon, outputFile string, webpFile string) {
	switch pack.FinalType {
	case "png":
		linf("COMPRESS TO WEBP %s/%s...", pack.Name, icon.Name)
		cmd := exec.Command("cwebp", outputFile, "-o", webpFile)
		var transpegOut bytes.Buffer
		cmd.Stderr = &transpegOut
		err := cmd.Run()
		if err != nil {
			lerr("ERROR")
			linf("RESULT\n%s", transpegOut.String())
		}
		checkErr(err)
		linf("OK")
	}
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
				svgzFile := path.Join(outputDir, icon.Name+".svgz")
				webpFile := path.Join(outputDir, icon.Name+".webp")
				linf("COPY %s/%s...", pack.Name, icon.Name)
				err := copyFile(inputFile, outputFile)
				if err != nil {
					lerr("ERROR")
				}
				checkErr(err)
				linf("OK")
				makeSvgz(pack, icon, outputFile, svgzFile)
				makeWebp(pack, icon, outputFile, webpFile)
			}
		} else if pack.InputType == "svg" && pack.FinalType == "png" {
			for _, icon := range pack.Icons {
				inputFile := path.Join(s.cfg.Input, pack.Name, icon.Name+"."+pack.InputType)
				outputFile := path.Join(outputDir, icon.Name+"."+pack.FinalType)
				svgzFile := path.Join(outputDir, icon.Name+".svgz")
				webpFile := path.Join(outputDir, icon.Name+".webp")
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
				makeSvgz(pack, icon, outputFile, svgzFile)
				makeWebp(pack, icon, outputFile, webpFile)
			}
		} else {
			checkErr(fmt.Errorf("ERROR: Cannot process %s → %s", pack.InputType, pack.FinalType))
		}

		inputFile := path.Join(s.cfg.Input, pack.Name, "banner.svg")
		outputFile := path.Join(outputDir, "banner.png")
		webpFile := path.Join(outputDir, "banner.webp")
		jpgFile := path.Join(outputDir, "banner.jpg")
		linf("CONV %s/banner...", pack.Name)
		cmd := exec.Command("inkscape", "-h", "900", inputFile, "--export-filename", outputFile)
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

		linf("COMPRESS %s/banner TO WEBP...", pack.Name)
		cmd = exec.Command("cwebp", outputFile, "-o", webpFile)
		var webpOut bytes.Buffer
		cmd.Stderr = &webpOut
		err = cmd.Run()
		if err != nil {
			lerr("ERROR")
			linf("RESULT\n%s", webpOut.String())
		}
		checkErr(err)
		linf("OK")

		linf("COMPRESS %s/banner TO JPEG...", pack.Name)
		cmd = exec.Command("convert", "-quality", "85", outputFile, jpgFile)
		var jpgOut bytes.Buffer
		cmd.Stderr = &jpgOut
		err = cmd.Run()
		if err != nil {
			lerr("ERROR")
			linf("RESULT\n%s", jpgOut.String())
		}
		checkErr(err)
		linf("OK")

		checkErr(os.RemoveAll(path.Join(s.cfg.Files, pack.Name)))
		checkErr(os.Rename(path.Join(outputFiles, pack.Name), path.Join(s.cfg.Files, pack.Name)))
	}
	checkErr(os.RemoveAll(outputFiles))
}

func (s *worker) fillPacks() {
	s.packs = make([]sitelib.Pack, len(s.cfg.Packs))
	for i, pack := range s.cfg.Packs {
		s.packs[i] = pack
	}
}

var verbose = flag.Bool("v", false, "verbose output")

func main() {
	flag.Parse()
	if flag.NArg() < 2 {
		panic("usage: chic-converter <config> <icons...>")
	}
	w := &worker{cfg: sitelib.ReadConfig(flag.Arg(0))}
	w.fillPacks()
	w.convert(flag.Args()[1:])
}
