package sitelib

import (
	"encoding/json"
	"errors"
	"os"
	"path"
	"sort"
	"strings"

	"github.com/bcmk/siren/lib"
)

// IconNamesSet represents all the icons in chic
var IconNamesSet = map[string]bool{}

func init() {
	var iconNames = []string{
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
	for _, i := range iconNames {
		IconNamesSet[i] = true
	}
}

// ParsePacks parses icons packs
func ParsePacks(dir string) []Pack {
	packs := []Pack{}
	files, err := os.ReadDir(dir)
	lib.CheckErr(err)
	for _, file := range files {
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
		iconFiles, err := os.ReadDir(packDir)
		lib.CheckErr(err)
		foundIcons := map[string]bool{}
		for _, iconFile := range iconFiles {
			iconFileName := iconFile.Name()
			iconName := iconFileName
			iconName = strings.TrimSuffix(iconName, ".svg")
			iconName = strings.TrimSuffix(iconName, ".png")
			if !IconNamesSet[iconName] {
				continue
			}
			if strings.HasSuffix(iconFileName, ".svg") {
				if parsed.InputType == "" {
					parsed.InputType = "svg"
				}
				if parsed.InputType != "svg" {
					lib.CheckErr(errors.New("incompatible icon type"))
				}
				foundIcons[iconName] = true
			}
			if strings.HasSuffix(iconFileName, ".png") {
				if parsed.InputType == "" {
					parsed.InputType = "png"
				}
				if parsed.InputType != "png" {
					lib.CheckErr(errors.New("incompatible icon type"))
				}
				foundIcons[iconName] = true
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
