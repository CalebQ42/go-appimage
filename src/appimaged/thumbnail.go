package main

import (
	"bytes"
	"fmt"
	"image"
	"image/png"
	"io"
	"log"
	"os"
	"time"

	"github.com/adrg/xdg"
	issvg "github.com/h2non/go-is-svg"
	"github.com/probonopd/go-appimage/internal/helpers"
	pngembed "github.com/sabhiram/png-embed" // For embedding metadata into PNG
	"github.com/srwiley/oksvg"
	"github.com/srwiley/rasterx"
)

/* The thumbnail cache directory is prefixed with $XDG_CACHE_DIR/ and the leading dot removed
(since $XDG_CACHE_DIR is normally $HOME/.cache).
The glib ChangeLog indicates the path for large sizes was "fixed" (Added $XDG_CACHE_DIR) starting with 2.35.3 */
var ThumbnailsDirNormal = xdg.CacheHome + "/thumbnails/normal/"

func (ai AppImage) extractDirIconAsThumbnail() {
	// log.Println("thumbnail: extract DirIcon as thumbnail")
	if ai.Type() <= 0 {
		return
	}

	// TODO: Detect Modifications by reading the 'Thumb::MTime' key as per
	// https://specifications.freedesktop.org/thumbnail-spec/thumbnail-spec-latest.html#MODIFICATIONS

	//this will try to extract the thumbnail, or goes back to command based extraction if it fails.
	buf := new(bytes.Buffer)
	var data []byte
	dirIconRdr, err := ai.Thumbnail()
	if err != nil {
		if *verbosePtr {
			log.Print("Could not find .DirIcon, trying to find the desktop file's specified icon")
		}
		dirIconRdr, _, err = ai.Icon()
		if err != nil {
			goto genericIcon
		}
	}
	_, err = io.Copy(buf, dirIconRdr)
	dirIconRdr.Close()
	if err != nil {
		helpers.LogError("thumbnail", err)
	}
genericIcon:
	if buf.Len() == 0 {
		if *verbosePtr == true {
			log.Println("Could not extract icon, use default icon instead")
		}
		data, err = Asset("data/appimage.png")
		helpers.LogError("thumbnail", err)
		buf = bytes.NewBuffer(data)
	}
	if issvg.Is(buf.Bytes()) {
		log.Println("thumbnail: .DirIcon in", ai.Path, "is an SVG, this is discouraged. Costly converting it now")
		buf, err = convertToPng(buf)
		helpers.LogError("thumbnail", err)
	}

	// Before we proceed, delete empty files. Otherwise the following operations can crash
	// TODO: Better check if it is a PNG indeed

	if buf.Len() == 0 {
		helpers.LogError("thumbnail", fmt.Errorf("No thumbnail"))
		return
	}

	// Write "Thumbnail Attributes" metadata as mandated by
	// https://specifications.freedesktop.org/thumbnail-spec/thumbnail-spec-latest.html#ADDINFOS
	// and set thumbnail permissions to 0600 as mandated by
	// https://specifications.freedesktop.org/thumbnail-spec/thumbnail-spec-latest.html#AEN245
	// Thumb::URI	The absolute canonical uri for the original file. (eg file:///home/jens/photo/me.jpg)

	// FIXME; github.com/sabhiram/png-embed does not overwrite pre-existing values,
	// https://github.com/sabhiram/png-embed/issues/1

	content, err := pngembed.Extract(buf.Bytes())
	if err == nil {
		if *verbosePtr == true {
			if _, ok := content["Thumb::URI"]; ok {
				log.Println("thumbnail: FIXME: Remove pre-existing Thumb::URI in", ai.Path)
				// log.Println(content["Thumb::URI"])
			}
			if _, ok := content["Thumb::MTime"]; ok {
				log.Println("thumbnail: FIXME: Remove pre-existing Thumb::MTime", content["Thumb::MTime"], "in", ai.Path) // FIXME; pngembed does not seem to overwrite pre-existing values, is it a bug there?
				// log.Println(content["Thumb::MTime"])
			}
		}
		helpers.LogError("thumbnail", err)
		data, err = pngembed.Embed(buf.Bytes(), "Thumb::URI", ai.uri)
		if err != nil {
			helpers.LogError("thumbnail", err)
			buf = bytes.NewBuffer(data)
		}
		/* Set 'Thumb::MTime' metadata of the thumbnail file to the mtime of the AppImage.
		NOTE: https://specifications.freedesktop.org/thumbnail-spec/thumbnail-spec-latest.html#MODIFICATIONS says:
		It is not sufficient to do a file.mtime > thumb.MTime check.
		If the user moves another file over the original, where the mtime changes but is in fact lower
		than the thumbnail stored mtime, we won't recognize this modification.
		If for some reason the thumbnail doesn't have the 'Thumb::MTime' key (although it's required)
		it should be recreated in any case. */
		data, err = pngembed.Embed(buf.Bytes(), "Thumb::MTime", ai.ModTime())
		if err != nil {
			helpers.LogError("thumbnail", err)
			buf = bytes.NewBuffer(data)
		}
	} else {
		helpers.LogError("thumbnail", err)
	}

	// Set thumbnail permissions to 0600 as mandated by
	// https://specifications.freedesktop.org/thumbnail-spec/thumbnail-spec-latest.html#AEN245
	// err = os.Chmod(thumbnailcachedir+"/.DirIcon", 0600)
	// printError("thumbnail", err)

	// After all the processing is done, move the icons to their real location
	// where they are (hopefully) picked up by the desktop environment
	err = os.MkdirAll(ThumbnailsDirNormal, os.ModePerm)
	helpers.LogError("thumbnail", err)

	if *verbosePtr == true {
		log.Println("thumbnail: Creating", ai.thumbnailfilepath)
	}

	helpers.LogError("thumbnail", err)

	thumbnail, err := os.Create(ai.thumbnailfilepath)
	if os.IsExist(err) {
		if *verbosePtr == true {
			log.Println("thumbnail:", ai.thumbnailfilepath, "exists. Deleting and writing.")
		}
		os.Remove(ai.thumbnailfilename)
		thumbnail, err = os.Create(ai.thumbnailfilepath)
		if err != nil {
			helpers.LogError("thumbnail", err)
			return
		}
	}
	_, err = io.Copy(thumbnail, buf)
	if err != nil {
		helpers.LogError("thumbnail", err)
		return
	}

	/* Also set mtime of the thumbnail file to the mtime of the AppImage. Quite possibly this is not needed.
	TODO: Perhaps we can remove it.
	See https://specifications.freedesktop.org/thumbnail-spec/thumbnail-spec-latest.html#MODIFICATIONS  */
	err = os.Chtimes(ai.thumbnailfilepath, time.Now().Local(), ai.ModTime())
	helpers.LogError("thumbnail", err)

	// In Xfce, the new thumbnail is not shown in the file manager until we touch the file
	// In fact, touching it from within this program makes the thumbnail not work at all
	// TODO: Read XDG Thumbnail spec regarding this
	// The following all does not work
	// time.Sleep(2 * time.Second)
	// now := time.Now()
	// err = os.Chtimes(ai.path, now, now)
	// printError("thumbnail", err)
	// cmd = exec.Command("touch", ai.thumbnailfilepath)
}

// Convert a given file into a PNG; its dependencies add about 2 MB to the executable
func convertToPng(reader io.Reader) (*bytes.Buffer, error) {
	icon, err := oksvg.ReadIconStream(reader, oksvg.WarnErrorMode)
	if err != nil {
		return nil, err
	}
	w, h := int(icon.ViewBox.W), int(icon.ViewBox.H)
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	scannerGV := rasterx.NewScannerGV(w, h, img, img.Bounds())
	raster := rasterx.NewDasher(w, h, scannerGV)
	icon.Draw(raster, 1.0)
	var out bytes.Buffer
	err = png.Encode(&out, img)
	if err != nil {
		return nil, err
	}
	return &out, nil
}
