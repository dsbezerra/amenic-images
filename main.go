package main

import (
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/disintegration/imaging"
	"github.com/fogleman/gg"
	"github.com/generaltso/vibrant"
)

var (
	BaseURL = "https://api.amenic.app"

	FolderImages = "./data/images/"
	FolderFonts  = "./data/fonts/"
	UsePalette   = false

	Logo           *image.NRGBA
	IconCinemais   *image.NRGBA
	IconIbicinemas *image.NRGBA
	ImageLoadMutex = &sync.RWMutex{}
)

type Inset struct {
	Left   float64
	Right  float64
	Top    float64
	Bottom float64
}

type Rect struct {
	Left   float64
	Right  float64
	Top    float64
	Bottom float64
	Width  float64
	Height float64
}

// DateRange ...
type DateRange struct {
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
}

// Home ...
type Home struct {
	NowPlayingWeek DateRange     `json:"now_playing_week"`
	Movies         []StaticMovie `json:"movies"`
}

// StaticMovie ...
type StaticMovie struct {
	Title       string    `json:"title"`
	PosterURL   string    `json:"poster"`
	ReleaseDate time.Time `json:"release_date"`
	Theatres    string    `json:"theatres"`
	MovieURL    string    `json:"movie_url"`
	SessionType int       `json:"session_type"`
}

func main() {
	home, err := getHome()
	if err != nil {
		log.Fatal(err)
	}

	createNowPlayingImages(home.NowPlayingWeek, home.Movies)
}

func createNowPlayingImages(week DateRange, movies []StaticMovie) {
	// Filter home movies to now playing only
	items := make([]StaticMovie, 0)
	for _, it := range movies {
		if it.Theatres != "" {
			items = append(items, it)
		} else {
			break
		}
	}

	size := len(items)
	if size == 0 {
		return
	}

	wg := &sync.WaitGroup{}

	rangeFormatted := formatDateRange(week, true)
	cols := 3
	created := 0
	for i := 0; i < size-1; i += cols {
		arr := make([]StaticMovie, 0)
		for n := 0; n < cols; n++ {
			if i+n >= size {
				break
			}
			arr = append(arr, items[i+n])
		}
		output := fmt.Sprintf("Em cartaz %s-%d.png", rangeFormatted, created+1)
		wg.Add(1)
		go asyncCreateNowPlayingImage(rangeFormatted, arr, output, wg)
		created++
	}
	wg.Wait()
}

func asyncCreateNowPlayingImage(week string, movies []StaticMovie, output string, wg *sync.WaitGroup) {
	createNowPlayingImage(week, movies, output)
	wg.Done()
}

func createNowPlayingImage(week string, movies []StaticMovie, output string) {
	count := len(movies)
	if len(movies) == 0 {
		return
	}

	fmt.Printf("Creating now playing image with %d movies...\n", count)

	width := 1000.0
	height := width

	inset := Inset{Left: 10.0, Right: 10.0}

	totalSpace := width - inset.Left - inset.Right
	itemw := totalSpace * 0.3

	// Download posters
	filenames := make([]string, count)
	wg := sync.WaitGroup{}
	for i, it := range movies {
		url := it.MovieURL
		idx := strings.LastIndex(url, "/")
		filename := url[idx+1:] + ".jpg"
		filenames[i] = "downloaded/" + filename
		wg.Add(1)
		go asyncDownloadImage(it.PosterURL, filename, &wg)
	}

	wg.Wait()

	dc := gg.NewContext(int(width), int(height))

	var backgroundImage image.Image

	// Load poster images
	posters := make([]image.Image, count)
	for i, f := range filenames {
		image, err := gg.LoadJPG(FolderImages + f)
		if err != nil {
			panic(err)
		}
		// NOTE(diego): First file == most recent movie
		if i == 0 {
			// Resize to width of image and blur
			backgroundImage = imaging.Blur(imaging.Resize(image, int(width*1.5), 0, imaging.Lanczos), 5)
		}
		posters[i] = imaging.Resize(image, int(itemw), 0, imaging.Lanczos)
	}
	itemh := float64(posters[0].Bounds().Dy())

	// Load fonts
	headerFont, err := gg.LoadFontFace(FolderFonts+"Roboto-Bold.ttf", 56)
	if err != nil {
		panic(err)
	}
	subHeaderFont, err := gg.LoadFontFace(FolderFonts+"Roboto-Regular.ttf", 32)
	if err != nil {
		panic(err)
	}
	movieTitleFont, err := gg.LoadFontFace(FolderFonts+"Roboto-Bold.ttf", 26)
	if err != nil {
		panic(err)
	}

	// Draw background
	//
	// Clear to background color
	dc.SetRGB255(33, 33, 33)
	dc.Clear()
	//
	dc.DrawImageAnchored(backgroundImage, int(width/2), int(height/2), 0.5, 0.5)
	grad := gg.NewLinearGradient(0, 0, 0, height)
	grad.AddColorStop(0.0, color.NRGBA{33, 33, 33, 50})
	grad.AddColorStop(0.5, color.NRGBA{33, 33, 33, 255})
	grad.AddColorStop(1, color.NRGBA{33, 33, 33, 255})
	dc.SetFillStyle(grad)
	dc.DrawRectangle(0.0, 0.0, width, height)
	dc.Fill()

	vspacing := 50.0
	y := vspacing

	cols := 3.0
	margin := (totalSpace - (itemw * cols)) / 2.0

	// Draw top bar
	y += DrawTopBar(dc, width, &backgroundImage) + vspacing

	// Apply everything else
	dc.Translate(margin, 0)

	// Draw Now Playing Header
	dc.SetColor(color.White)
	dc.SetFontFace(headerFont)
	dc.DrawString("Em cartaz", 0, y)
	y += vspacing

	// Draw Sub Header
	dc.SetRGBA255(255, 255, 255, 170) // #aaffffff
	dc.SetFontFace(subHeaderFont)
	dc.DrawString(week, 0, y-10)
	y += vspacing

	// Draw movie items
	ensureTheaterIconsAreLoaded(int(itemw * 0.1))

	i := 0
	x := 0.0

	titleWidth := itemw - inset.Left*2 - inset.Right*2
	titleHeight := 0.0
	dc.SetFontFace(movieTitleFont)
	for _, it := range movies {
		result := dc.WordWrap(it.Title, titleWidth)
		_, th := dc.MeasureMultilineString(strings.Join(result, "\n"), 1.2)
		if th > titleHeight {
			titleHeight = th
		}
	}

	space := 10.0

	// Center posters
	totalWidthOfItems := (float64(count) * itemw) + (float64(count-1) * space)
	actualWidth := width - margin - margin
	tx := actualWidth/2 - totalWidthOfItems/2
	dc.Translate(tx, 0)

	for i < count {
		m := movies[i]

		top := y
		left := x
		right := left + itemw
		bottom := top + itemh

		// Draw poster
		poster := posters[i]

		// Draw top-left and top-right rounded rectangle
		r := itemw * 0.05
		x0, x1, x2, x3 := x, x+r, x+itemw-r, x+itemw
		y0, y1, y2, y3 := y, y+r, y+itemh-r, y+itemh
		dc.NewSubPath()
		dc.MoveTo(x1, y0)
		dc.LineTo(x2, y0)
		dc.DrawArc(x2, y1, r, gg.Radians(270), gg.Radians(360))
		dc.LineTo(x3, y3)
		dc.LineTo(x0, y3)
		dc.LineTo(x0, y2)
		dc.DrawArc(x1, y1, r, gg.Radians(180), gg.Radians(270))
		dc.ClosePath()
		dc.Clip()

		// Draw image
		cx := left + float64(itemw)/2.0
		cy := top + float64(itemh)/2.0
		dc.DrawImageAnchored(poster, int(cx), int(cy), 0.5, 0.5)

		// Draw gradient overlay
		grad := gg.NewLinearGradient(0, top, 0, bottom)
		grad.AddColorStop(0, color.NRGBA{33, 33, 33, 0})
		grad.AddColorStop(0.5, color.NRGBA{33, 33, 33, 15})
		grad.AddColorStop(1, color.NRGBA{33, 33, 33, 250})
		dc.SetFillStyle(grad)
		dc.DrawRectangle(x, y, right-left, bottom-top)
		dc.Fill()

		dc.ResetClip()

		// Draw title
		titleTop := bottom - titleHeight - 40.0
		dc.SetColor(color.White)
		dc.SetFontFace(movieTitleFont)
		dc.DrawStringWrapped(m.Title, x+inset.Left*2, titleTop, 0, 0, titleWidth, 1.2, gg.AlignLeft)

		// Draw theaters
		theaters := strings.Split(m.Theatres, " - ")
		theatersTop := int(titleTop + titleHeight + space*3.0)
		theatersLeft := int(x + inset.Left*2)
		for _, t := range theaters {
			im := IconCinemais
			if t == "IBICINEMAS" {
				im = IconIbicinemas
			}
			dc.DrawImage(im, theatersLeft, theatersTop)
			theatersLeft += im.Bounds().Dx() + int(space)
		}

		x += space + itemw
		i++
	}

	err = dc.SavePNG(output)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println("Now playing image created!")
}

// DrawTopBar draws the top bar with logo
func DrawTopBar(dc *gg.Context, width float64, extractTarget *image.Image) float64 {
	ensureLogoIsLoaded()

	c := color.RGBA{33, 33, 33, 255}
	if UsePalette && extractTarget != nil {
		palette, err := vibrant.NewPaletteFromImage(*extractTarget)
		if err == nil {
			for name, swatch := range palette.ExtractAwesome() {
				if name == "DarkMuted" {
					r, g, b := swatch.Color.RGB()
					c = color.RGBA{uint8(r), uint8(g), uint8(b), 255}
					break
				}
			}
		}
	}

	paddingTop := 40
	paddingBottom := 40

	logoHeight := Logo.Bounds().Dy()
	barHeight := float64(logoHeight + paddingTop + paddingBottom)

	dc.SetColor(c)
	dc.DrawRectangle(0, 0, width, barHeight)
	dc.Fill()

	dc.DrawImageAnchored(Logo, int(width/2), int(barHeight/2), 0.5, 0.5)

	return barHeight
}

func ensureLogoIsLoaded() {
	ImageLoadMutex.Lock()
	if Logo == nil {
		im, err := gg.LoadImage(FolderImages + "logo.png")
		if err != nil {
			panic(err)
		}
		Logo = imaging.Resize(im, 120, 0, imaging.Lanczos)
	}
	ImageLoadMutex.Unlock()
}

func ensureTheaterIconsAreLoaded(width int) {
	theaters := []string{}

	ImageLoadMutex.Lock()
	if IconCinemais == nil {
		theaters = append(theaters, "cinemais")
	}
	if IconIbicinemas == nil {
		theaters = append(theaters, "ibicinemas")
	}

	if len(theaters) > 0 {
		for _, t := range theaters {
			im, err := gg.LoadPNG(FolderImages + "ic_" + t + ".png")
			if err != nil {
				panic(err)
			}

			switch t {
			case "cinemais":
				IconCinemais = imaging.Resize(im, width, 0, imaging.Lanczos)
			case "ibicinemas":
				IconIbicinemas = imaging.Resize(im, width, 0, imaging.Lanczos)
			}
		}
	}
	ImageLoadMutex.Unlock()
}

func getHome() (*Home, error) {
	var result *Home

	res, err := http.Get(fmt.Sprintf("%s/home.json", BaseURL))
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()

	if res.StatusCode == 200 {
		content, err := ioutil.ReadAll(res.Body)
		if err != nil {
			return nil, err
		}

		err = json.Unmarshal(content, &result)
		if err != nil {
			return nil, err
		}

		return result, nil
	}

	return nil, fmt.Errorf("response %s", res.Status)
}

func asyncDownloadImage(url string, filename string, wg *sync.WaitGroup) {
	downloadImage(url, filename)
	wg.Done()
}

func downloadImage(url string, filename string) string {
	filepath := FolderImages + "/downloaded/" + filename
	if _, err := os.Stat(filepath); os.IsNotExist(err) {
		res, err := http.Get(url)
		if err != nil {
			panic(err)
		}

		defer res.Body.Close()

		data, err := ioutil.ReadAll(res.Body)
		if err != nil {
			panic(err)
		}

		err = ioutil.WriteFile(filepath, data, os.ModePerm)
		if err != nil {
			panic(err)
		}
	}

	return filepath
}

func getPosterDimensions(image image.Image) (int, int) {
	bounds := image.Bounds()
	return bounds.Dx(), bounds.Dy()
}

func getMonthText(m time.Month) string {
	switch m {
	case time.January:
		return "Janeiro"
	case time.February:
		return "Fevereiro"
	case time.March:
		return "Março"
	case time.April:
		return "Abril"
	case time.May:
		return "Maio"
	case time.June:
		return "Junho"
	case time.July:
		return "Julho"
	case time.August:
		return "Agosto"
	case time.September:
		return "Setembro"
	case time.October:
		return "Outubro"
	case time.November:
		return "Novembro"
	case time.December:
		return "Dezembro"
	default:
		return ""
	}
}

func formatDateRange(dr DateRange, lowercased bool) string {
	sm, sd := dr.Start.Month(), dr.Start.Day()
	em, ed := dr.End.Month(), dr.End.Day()
	endMonth := getMonthText(em)
	if lowercased {
		endMonth = strings.ToLower(endMonth)
	}
	if sm != em {
		startMonth := getMonthText(sm)
		if lowercased {
			startMonth = strings.ToLower(startMonth)
		}
		return fmt.Sprintf("%d de %s a %d de %s", sd, startMonth, ed, endMonth)
	}
	return fmt.Sprintf("%d a %d de %s", sd, ed, endMonth)
}
