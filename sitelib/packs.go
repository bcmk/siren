package sitelib

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"image"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/s3"
	"github.com/bcmk/siren/lib/cmdlib"
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

func parseInputIcon(parsedPack *Pack, iconFileSetNeedle map[string]bool, packDir string, iconFileName string) *Icon {
	if !iconFileSetNeedle[iconFileName] {
		return nil
	}

	width, height := .0, .0
	switch parsedPack.InputType {
	case "svg":
		width, height = parseSVGSize(path.Join(packDir, iconFileName))
	case "png":
		width, height = parsePNGSize(path.Join(packDir, iconFileName))
	}

	return &Icon{
		NotVersionedName: strings.TrimSuffix(iconFileName, "."+parsedPack.InputType),
		Width:            width,
		Height:           height,
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
	switch parsedPack.FinalType {
	case "svg":
		width, height = parseSVGSize(path.Join(packDir, iconFileName))
	case "png":
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
	cmdlib.CheckErr(err)
	decoder := json.NewDecoder(configFile)
	parsedPack := Pack{}
	err = decoder.Decode(&parsedPack)
	cmdlib.CheckErr(err)
	cmdlib.CheckErr(configFile.Close())
	parsedPack.Name = packName
	if parsedPack.ChaturbateIconsScale == nil {
		parsedPack.ChaturbateIconsScale = &parsedPack.Scale
	}
	iconFiles, err := os.ReadDir(packDir)
	cmdlib.CheckErr(err)
	foundInputIcons := map[string]Icon{}
	foundFinalIcons := map[string]Icon{}
	inputIconFileSetNeedle := buildInputIconFileSetNeedle(&parsedPack)
	finalIconFileSetNeedle := buildFinalIconFileSetNeedle(&parsedPack)
	for _, iconFile := range iconFiles {
		inputIcon := parseInputIcon(&parsedPack, inputIconFileSetNeedle, packDir, iconFile.Name())
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
	cmdlib.Linf("configured %s v%d, input: %s\n", packName, parsedPack.Version, parsedPack.InputType)
	return parsedPack
}

// ParsePacks parses icons packs
func ParsePacks(dir string) []Pack {
	var packs []Pack
	files, err := os.ReadDir(dir)
	cmdlib.CheckErr(err)
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

func download(svc *s3.S3, bucketName string, key *string) (*bytes.Buffer, error) {
	out, err := svc.GetObject(&s3.GetObjectInput{
		Bucket: aws.String(bucketName),
		Key:    key,
	})
	if err != nil {
		return nil, err
	}
	defer func() { cmdlib.CheckErr(out.Body.Close()) }()

	var buf bytes.Buffer
	if _, err := io.Copy(&buf, out.Body); err != nil {
		return nil, err
	}

	return &buf, nil
}

// ParsePacksV2 parses icons packs for config V2
func ParsePacksV2(config *Config) []PackV2 {
	sess, err := session.NewSession(&aws.Config{
		Region:           aws.String(config.BucketRegion),
		Endpoint:         aws.String(config.BucketEndpoint),
		S3ForcePathStyle: aws.Bool(true),
		Credentials:      credentials.NewStaticCredentials(config.BucketAccessKey, config.BucketSecretKey, ""),
	})
	cmdlib.CheckErr(err)
	svc := s3.New(sess)
	var packs []PackV2
	err = svc.ListObjectsV2Pages(&s3.ListObjectsV2Input{
		Bucket: aws.String(config.BucketName),
	}, func(page *s3.ListObjectsV2Output, _ bool) bool {
		for _, obj := range page.Contents {
			if strings.HasSuffix(*obj.Key, "/config_v2.json") {
				fmt.Printf("Parsing %s...\n", *obj.Key)
				buf, err := download(svc, config.BucketName, obj.Key)
				cmdlib.CheckErr(err)
				var pack PackV2
				cmdlib.CheckErr(json.Unmarshal(buf.Bytes(), &pack))
				fullDirPath := filepath.Dir(*obj.Key)
				dirName := filepath.Base(fullDirPath)
				pack.Name = dirName
				packs = append(packs, pack)
			}
		}
		return true
	})
	cmdlib.CheckErr(err)
	sort.Slice(packs, func(i, j int) bool { return packs[i].Timestamp < packs[j].Timestamp })
	return packs
}

// parsePNGSize parses the PNG file and returns its width and height
func parsePNGSize(filename string) (width, height float64) {
	file, err := os.Open(filename)
	cmdlib.CheckErr(err)
	defer func() { cmdlib.CheckErr(file.Close()) }()
	img, _, err := image.Decode(file)
	cmdlib.CheckErr(err)
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
	cmdlib.CheckErr(err)
	defer func() { cmdlib.CheckErr(file.Close()) }()
	var svg SVG
	decoder := xml.NewDecoder(file)
	err = decoder.Decode(&svg)
	cmdlib.CheckErr(err)
	width, err = strconv.ParseFloat(svg.Width, 64)
	cmdlib.CheckErr(err)
	height, err = strconv.ParseFloat(svg.Height, 64)
	cmdlib.CheckErr(err)
	return width, height
}
