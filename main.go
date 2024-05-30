package main

import (
	"errors"
	"flag"
	"fmt"
	"image"
	"image/color/palette"
	"image/draw"
	"image/gif"
	"image/png"
	"log"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/kettek/apng"
	"github.com/sunshineplan/weather/api/zoomearth"
	"github.com/sunshineplan/weather/maps"
	"github.com/sunshineplan/weather/storm"
	"github.com/sunshineplan/weather/unit/coordinates"
)

const (
	keep        = 432
	radius      = 700.0
	format      = "200601021504"
	shortFormat = "01021504"
)

var (
	api      = zoomearth.ZoomEarthAPI{}
	shanghai = coordinates.New(31.228611, 121.474722)
	timezone = time.FixedZone("CST", 8*60*60)
	opt      = zoomearth.NewMapOptions().
			SetSize(800, 800).SetZoom(5).
			SetOverlays([]string{"radar", "wind"}).
			SetTimeZone(timezone)
)

func main() {
	flag.Parse()
	switch flag.Arg(0) {
	case "", "satellite":
		if err := satellite(time.Time{}, shanghai, "satellite", format); err != nil {
			if pathError, ok := err.(*os.PathError); ok {
				log.Print(pathError)
			} else {
				log.Fatal(err)
			}
		}
		for _, t := range getTimes("satellite", format) {
			if err := satellite(t, shanghai, "satellite", format); err != nil {
				log.Fatal(err)
			}
		}
		for _, d := range []time.Duration{72, 48, 24, 12, 6} {
			d = d * time.Hour
			gifImg, apngImg, err := animation("satellite/*", d, true)
			if err != nil {
				log.Fatal(err)
			}
			name := "satellite-" + strings.TrimSuffix(d.String(), "0m0s")
			if err := save(name+".gif", gifImg); err != nil {
				log.Fatal(err)
			}
			if err := save(name+".png", apngImg); err != nil {
				log.Fatal(err)
			}
		}
	case "typhoon":
		res, err := typhoon()
		if err != nil {
			log.Fatal(err)
		}
		if len(res) == 0 {
			log.Print("no typhoon found")
		}
		for _, i := range res {
			log.Printf("found: %s(%d-%s)", i.Name, i.No, i.ID)
			dir := filepath.Join("typhoon", strconv.Itoa(time.Now().In(timezone).Year()), fmt.Sprintf("%d-%s", i.No, i.ID))
			if err := satellite(time.Time{}, i.Coordinates(time.Now()), dir, shortFormat); err != nil {
				log.Print(err)
				continue
			}
			for _, t := range getTimes(dir, shortFormat) {
				if coords := i.Coordinates(t); coords != nil {
					if err := satellite(t, coords, dir, shortFormat); err != nil {
						log.Print(err)
						continue
					}
				}
			}
			gifImg, apngImg, err := animation(dir+"/*.png", 0, false)
			if err != nil {
				log.Print(err)
				continue
			}
			if err := save(dir+".gif", gifImg); err != nil {
				log.Print(err)
				continue
			}
			if err := save(dir+".png", apngImg); err != nil {
				log.Print(err)
			}
		}
	default:
		log.Fatal("bad command")
	}
}

func satellite(t time.Time, coords coordinates.Coordinates, path, format string) (err error) {
	t, img, err := api.Map(maps.Satellite, t, coords, opt)
	if err != nil {
		if errors.Is(err, maps.ErrInsufficientColor) {
			log.Print(err)
		} else {
			return
		}
	}
	if err = os.MkdirAll(path, 0755); err != nil {
		return
	}
	file := filepath.Join(path, t.Format(format)+".png")
	f, err := os.OpenFile(file, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0666)
	if err != nil {
		return
	}
	defer f.Close()
	if err = png.Encode(f, img); err != nil {
		return
	}
	log.Print(file)
	return
}

func typhoon() (res []storm.Data, err error) {
	typhoons, err := api.GetStorms(time.Now())
	if err != nil {
		return
	}
	for _, i := range typhoons {
		typhoon, err := i.Data()
		if err != nil {
			log.Print(err)
			continue
		}
		if typhoon.Season == "" || typhoon.No == 0 {
			continue
		}
		if affect, _ := typhoon.Affect(shanghai, radius); affect {
			res = append(res, typhoon)
		}
	}
	return
}

func getTimes(path, format string) (ts []time.Time) {
	res, err := filepath.Glob(path + "/*.png")
	if err != nil {
		panic(err)
	}
	if len(res) == 0 {
		return
	}
	last, err := time.ParseInLocation(format, strings.TrimSuffix(filepath.Base(res[len(res)-1]), ".png"), timezone)
	if err != nil {
		panic(err)
	}
	for i := time.Duration(1); i <= 24; i++ {
		if t := last.Add(-i * 10 * time.Minute); slices.IndexFunc(res, func(s string) bool {
			return strings.HasSuffix(s, t.Format(format)+".png")
		}) == -1 {
			ts = append(ts, t)
		}
	}
	return
}

func animation(path string, d time.Duration, remove bool) (*gif.GIF, apng.APNG, error) {
	res, err := filepath.Glob(path)
	if err != nil {
		return nil, apng.APNG{}, err
	}
	if remove {
		for ; len(res) > keep; res = res[1:] {
			if err := os.Remove(res[0]); err != nil {
				log.Print(err)
			}
		}
	}
	if d != 0 {
		now := time.Now().In(timezone)
		res = slices.DeleteFunc(res, func(i string) bool {
			file := filepath.Base(i)
			if index := strings.LastIndex(file, "."); index != -1 {
				file = file[:index]
			}
			t, err := time.ParseInLocation(format, file, timezone)
			if err != nil {
				log.Print(err)
				return true
			}
			return now.Sub(t) > d
		})
	}
	var step int
	if d != 0 {
		step = int(d/time.Hour) / 3
	} else if step = int(math.Round(math.Log(1+float64(len(res))))) - 2; step <= 0 {
		step = 1
	}
	log.Println("step", step)
	var imgs []image.Image
	for i, name := range res {
		if i%step == 0 || i == len(res)-1 {
			f, err := os.Open(name)
			if err != nil {
				return nil, apng.APNG{}, err
			}
			defer f.Close()
			img, _, err := image.Decode(f)
			if err != nil {
				return nil, apng.APNG{}, err
			}
			imgs = append(imgs, img)
		}
	}
	gifImg, apngImg, n := new(gif.GIF), apng.APNG{}, len(imgs)
	var delay int
	if d != 0 {
		delay = 40
	} else if delay = 6000 / n; delay > 40 {
		delay = 40
	}
	for i, img := range imgs {
		p := image.NewPaletted(img.Bounds(), palette.Plan9)
		draw.Draw(p, p.Rect, img, image.Point{}, draw.Over)
		gifImg.Image = append(gifImg.Image, p)
		if i != n-1 {
			gifImg.Delay = append(gifImg.Delay, delay)
			apngImg.Frames = append(apngImg.Frames, apng.Frame{Image: img, DelayNumerator: uint16(delay)})
		} else {
			gifImg.Delay = append(gifImg.Delay, 300)
			apngImg.Frames = append(apngImg.Frames, apng.Frame{Image: img, DelayNumerator: 300})
		}
	}
	return gifImg, apngImg, nil
}

func save(name string, img any) error {
	log.Print(name)
	f, err := os.Create(name)
	if err != nil {
		return err
	}
	defer f.Close()
	switch img := img.(type) {
	case *gif.GIF:
		return gif.EncodeAll(f, img)
	case apng.APNG:
		return apng.Encode(f, img)
	default:
	}
	return nil
}
