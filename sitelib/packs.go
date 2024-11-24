package sitelib

import (
	"encoding/json"
	"encoding/xml"
	"image"
	"os"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/bcmk/siren/lib"
)

// IconNames represents all the icons in chic
var IconNames = []string{
	"siren",
	"fanclub",
	"instagram",
	"twitter",
	"onlyfans",
	"amazon",
	"lovense",
	"gift",
	"pornhub",
	"dmca",
	"allmylinks",
	"onemylink",
	"linktree",
	"fancentro",
	"frisk",
	"fansly",
	"throne",
	"mail",
	"snapchat",
	"telegram",
	"whatsapp",
	"youtube",
	"tiktok",
	"reddit",
	"twitch",
	"discord",
	"manyvids",
	"avn",
}

// ParsePacks parses icons packs
func ParsePacks(dir string) []Pack {
	var packs []Pack
	files, err := os.ReadDir(dir)
	lib.CheckErr(err)
	for _, file := range files {
		if !file.IsDir() {
			continue
		}
		packName := file.Name()
		if strings.HasPrefix(packName, "data-") || strings.HasPrefix(packName, "skip-") {
			continue
		}
		packDir := path.Join(dir, packName)
		configFile, err := os.Open(path.Join(packDir, "config.json"))
		lib.CheckErr(err)
		decoder := json.NewDecoder(configFile)
		parsed := Pack{}
		err = decoder.Decode(&parsed)
		lib.CheckErr(err)
		lib.CheckErr(configFile.Close())
		parsed.Name = packName
		if parsed.ChaturbateIconsScale == nil {
			parsed.ChaturbateIconsScale = &parsed.Scale
		}
		iconFiles, err := os.ReadDir(packDir)
		lib.CheckErr(err)
		foundIcons := map[string]Icon{}
		outputIconNameSet := map[string]bool{}
		for _, i := range IconNames {
			if parsed.Version == 0 {
				outputIconNameSet[i] = true
			} else {
				outputIconNameSet[i+".v"+strconv.Itoa(parsed.Version)] = true
			}
		}
		for _, iconFile := range iconFiles {
			iconFileName := iconFile.Name()
			versionedIconName := iconFileName
			versionedIconName = strings.TrimSuffix(versionedIconName, ".svg")
			versionedIconName = strings.TrimSuffix(versionedIconName, ".png")
			notVersionedIconName := removeVersion(versionedIconName)
			if !outputIconNameSet[versionedIconName] {
				continue
			}
			if strings.HasSuffix(iconFileName, ".svg") {
				width, height := parseSVGSize(path.Join(packDir, iconFileName))
				foundIcons[notVersionedIconName] = Icon{
					Name:   versionedIconName,
					Width:  width,
					Height: height,
				}
			}
			if strings.HasSuffix(iconFileName, ".png") {
				width, height := parsePNGSize(path.Join(packDir, iconFileName))
				foundIcons[notVersionedIconName] = Icon{
					Name:   versionedIconName,
					Width:  width,
					Height: height,
				}
			}
		}
		parsed.Icons = foundIcons
		parsed.VisibleIcons = map[string]bool{}
		hiddenIcons := map[string]bool{}
		for _, i := range parsed.HiddenIcons {
			hiddenIcons[i] = true
		}
		for i := range foundIcons {
			if !hiddenIcons[i] {
				parsed.VisibleIcons[i] = true
			}
		}
		lib.Linf("configured %s v%d, input: %s\n", packName, parsed.Version, parsed.InputType)
		packs = append(packs, parsed)
	}
	sort.Slice(packs, func(i, j int) bool { return packs[i].Timestamp < packs[j].Timestamp })
	return packs
}

// removeVersion removes the version from the end of icon name, like .v2
func removeVersion(name string) string {
	re := regexp.MustCompile(`\.v\d+$`)
	return re.ReplaceAllString(name, "")
}

// parsePNGSize parses the PNG file and returns its width and height
func parsePNGSize(filename string) (width, height float64) {
	file, err := os.Open(filename)
	lib.CheckErr(err)
	defer func() { lib.CheckErr(file.Close()) }()
	img, _, err := image.Decode(file)
	lib.CheckErr(err)
	return float64(img.Bounds().Dx()), float64(img.Bounds().Dy())
}

// SVG represents the structure of an SVG file
type SVG struct {
	Width  string `xml:"width,attr"`
	Height string `xml:"height,attr"`
}

// parseSVGSize parses the SVG file and returns its width and height
func parseSVGSize(filename string) (width, height float64) {
	file, err := os.Open(filename)
	lib.CheckErr(err)
	defer func() { lib.CheckErr(file.Close()) }()
	var svg SVG
	decoder := xml.NewDecoder(file)
	err = decoder.Decode(&svg)
	lib.CheckErr(err)
	width, err = strconv.ParseFloat(svg.Width, 64)
	lib.CheckErr(err)
	height, err = strconv.ParseFloat(svg.Height, 64)
	lib.CheckErr(err)
	return width, height
}
