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

func buildInputIconFileSetNeedle(parsedPack *Pack) map[string]bool {
	fileSet := map[string]bool{}
	for _, i := range IconNames {
		fileSet[i+"."+parsedPack.InputType] = true
	}
	return fileSet
}

func parseInputIcon(parsedPack *Pack, iconFileSetNeedle map[string]bool, iconFileName string) *Icon {
	if !iconFileSetNeedle[iconFileName] {
		return nil
	}

	return &Icon{
		NotVersionedName: strings.TrimSuffix(iconFileName, "."+parsedPack.InputType),
	}
}

func buildFinalIconFileSetNeedle(parsedPack *Pack) map[string]bool {
	fileSet := map[string]bool{}
	for _, i := range IconNames {
		if parsedPack.Version == 0 {
			fileSet[i+"."+parsedPack.FinalType] = true
		} else {
			fileSet[i+".v"+strconv.Itoa(parsedPack.Version)+"."+parsedPack.FinalType] = true
		}
	}
	return fileSet
}

func parseFinalIcon(parsedPack *Pack, iconFileSetNeedle map[string]bool, packDir string, iconFileName string) *Icon {
	if !iconFileSetNeedle[iconFileName] {
		return nil
	}

	width, height := .0, .0
	if parsedPack.FinalType == "svg" {
		width, height = parseSVGSize(path.Join(packDir, iconFileName))
	} else if parsedPack.FinalType == "png" {
		width, height = parsePNGSize(path.Join(packDir, iconFileName))
	}

	versionedIconName := strings.TrimSuffix(iconFileName, "."+parsedPack.FinalType)
	return &Icon{
		VersionedName:    versionedIconName,
		NotVersionedName: removeVersion(versionedIconName),
		Width:            width,
		Height:           height,
	}
}

// removeVersion removes the version from the end of icon name, like .v2
func removeVersion(name string) string {
	re := regexp.MustCompile(`\.v\d+$`)
	return re.ReplaceAllString(name, "")
}

func fillVisibleIcons(parsedPack *Pack, foundFinalIcons map[string]Icon) {
	hiddenIcons := map[string]bool{}
	for _, i := range parsedPack.HiddenIcons {
		hiddenIcons[i] = true
	}
	for i := range foundFinalIcons {
		if !hiddenIcons[i] {
			parsedPack.VisibleIcons[i] = true
		}
	}
}

func parsePack(libraryDir, packName string) Pack {
	packDir := path.Join(libraryDir, packName)
	configFile, err := os.Open(path.Join(packDir, "config.json"))
	lib.CheckErr(err)
	decoder := json.NewDecoder(configFile)
	parsedPack := Pack{}
	err = decoder.Decode(&parsedPack)
	lib.CheckErr(err)
	lib.CheckErr(configFile.Close())
	parsedPack.Name = packName
	if parsedPack.ChaturbateIconsScale == nil {
		parsedPack.ChaturbateIconsScale = &parsedPack.Scale
	}
	iconFiles, err := os.ReadDir(packDir)
	lib.CheckErr(err)
	foundInputIcons := map[string]Icon{}
	foundFinalIcons := map[string]Icon{}
	inputIconFileSetNeedle := buildInputIconFileSetNeedle(&parsedPack)
	finalIconFileSetNeedle := buildFinalIconFileSetNeedle(&parsedPack)
	for _, iconFile := range iconFiles {
		inputIcon := parseInputIcon(&parsedPack, inputIconFileSetNeedle, iconFile.Name())
		if inputIcon != nil {
			foundInputIcons[inputIcon.NotVersionedName] = *inputIcon
		}
		finalIcon := parseFinalIcon(&parsedPack, finalIconFileSetNeedle, packDir, iconFile.Name())
		if finalIcon != nil {
			foundFinalIcons[finalIcon.NotVersionedName] = *finalIcon
		}
	}
	parsedPack.InputIcons = foundInputIcons
	parsedPack.FinalIcons = foundFinalIcons
	parsedPack.VisibleIcons = map[string]bool{}
	fillVisibleIcons(&parsedPack, foundFinalIcons)
	lib.Linf("configured %s v%d, input: %s\n", packName, parsedPack.Version, parsedPack.InputType)
	return parsedPack
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
		parsed := parsePack(dir, packName)
		packs = append(packs, parsed)
	}
	sort.Slice(packs, func(i, j int) bool { return packs[i].Timestamp < packs[j].Timestamp })
	return packs
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
