package electricity

import (
	"bytes"
	"fmt"
	"sort"
	"time"

	chart "github.com/wcharczuk/go-chart/v2"
	"github.com/wcharczuk/go-chart/v2/drawing"
)

// tier colors (same as the Node version)
var (
	colorCheap     = drawing.Color{R: 0x00, G: 0xAA, B: 0x00, A: 0xff}
	colorNormal    = drawing.Color{R: 0xDD, G: 0x77, B: 0x00, A: 0xff}
	colorExpensive = drawing.Color{R: 0xDD, G: 0x00, B: 0x00, A: 0xff}
	colorGrid      = drawing.Color{R: 0xC8, G: 0xC8, B: 0xC8, A: 0x80}
	colorAnnotate  = drawing.Color{R: 0x00, G: 0x00, B: 0xEE, A: 0xb3}
)

// tierForPrice returns the price tier color (€/kWh)
func tierForPrice(priceKWh float64) drawing.Color {
	switch {
	case priceKWh < 0.10:
		return colorCheap
	case priceKWh <= 0.15:
		return colorNormal
	default:
		return colorExpensive
	}
}

// tierRun is a contiguous run of same-tier points drawn as one series
type tierRun struct {
	name    string
	color   drawing.Color
	xValues []float64
	yValues []float64
}

// renderChart renders the price chart as PNG (ported from the Node canvas renderer)
func (s *Service) renderChart(data *chartData) ([]byte, error) {
	s.log.Info("Rendering chart", "dual_day", data.IsDualDay, "points", len(data.Points))

	if len(data.Points) == 0 {
		return nil, fmt.Errorf("no data available for chart")
	}

	// Sort by datetime
	points := make([]PricePoint, len(data.Points))
	copy(points, data.Points)
	sort.Slice(points, func(i, j int) bool {
		return points[i].Datetime.Before(points[j].Datetime)
	})

	madrid := madridLocation()
	prices := make([]float64, len(points))
	for i, p := range points {
		prices[i] = p.Value / 1000 // €/kWh
	}

	width := 800
	labelStep := 3
	if data.IsDualDay {
		width = 1200
		labelStep = 4
	}

	// Split into contiguous same-tier runs; the legend entry is set on the
	// first run of each tier
	runs := buildTierRuns(prices)

	series := make([]chart.Series, 0, len(runs)+1)
	for _, run := range runs {
		series = append(series, chart.ContinuousSeries{
			Name:    run.name,
			XValues: run.xValues,
			YValues: run.yValues,
			Style: chart.Style{
				StrokeColor: run.color,
				StrokeWidth: 3,
				DotWidth:    5,
				DotColor:    run.color,
			},
		})
	}

	// Annotations: current hour + midnight separator
	annotations := buildAnnotations(points, data)
	if len(annotations) > 0 {
		series = append(series, chart.AnnotationSeries{
			Annotations: annotations,
			Style: chart.Style{
				StrokeColor:     colorAnnotate,
				StrokeDashArray: []float64{5, 5},
				StrokeWidth:     2,
			},
		})
	}

	title := "Precio de la electricidad: " + data.TodayDate
	if data.IsDualDay {
		title = "Precio de la electricidad: " + data.TodayDate + " a " + data.TomorrowDate
	}

	graph := chart.Chart{
		Width:  width,
		Height: 600,
		Title:  title,
		TitleStyle: chart.Style{
			FontSize:  18,
			FontColor: chart.ColorBlack,
		},
		Background: chart.Style{
			FillColor: chart.ColorWhite,
		},
		XAxis: chart.XAxis{
			Style:          chart.Style{StrokeColor: chart.ColorBlack},
			Ticks:          buildTicks(points, labelStep, madrid),
			GridMajorStyle: chart.Style{StrokeColor: colorGrid},
		},
		YAxis: chart.YAxis{
			Name:  "€/kWh",
			Style: chart.Style{StrokeColor: chart.ColorBlack},
			ValueFormatter: func(v interface{}) string {
				if f, ok := v.(float64); ok {
					return fmt.Sprintf("%.3f", f)
				}
				return fmt.Sprintf("%v", v)
			},
			GridMajorStyle: chart.Style{StrokeColor: colorGrid},
		},
		Series: series,
	}

	graph.Elements = []chart.Renderable{chart.Legend(&graph)}

	var buf bytes.Buffer
	if err := graph.Render(chart.PNG, &buf); err != nil {
		return nil, fmt.Errorf("failed to render chart: %w", err)
	}

	s.log.Info("PNG chart rendered successfully", "bytes", buf.Len())
	return buf.Bytes(), nil
}

// buildTierRuns groups the points into contiguous same-tier runs.
// The first run of each tier carries the legend name.
func buildTierRuns(prices []float64) []tierRun {
	legendNames := map[string]string{
		"cheap":     "Barato: < 0.10 €/kWh",
		"normal":    "Normal: 0.10-0.15 €/kWh",
		"expensive": "Caro: > 0.15 €/kWh",
	}
	tierOf := func(color drawing.Color) string {
		switch color {
		case colorCheap:
			return "cheap"
		case colorNormal:
			return "normal"
		default:
			return "expensive"
		}
	}

	var runs []tierRun
	named := map[string]bool{}

	currentColor := tierForPrice(prices[0])
	current := tierRun{color: currentColor}
	current.xValues = append(current.xValues, 0)
	current.yValues = append(current.yValues, prices[0])

	for i := 1; i < len(prices); i++ {
		color := tierForPrice(prices[i])
		// Close the current run when the tier changes, sharing the boundary
		// point so the line stays connected
		current.xValues = append(current.xValues, float64(i))
		current.yValues = append(current.yValues, prices[i])
		if color != currentColor {
			runs = append(runs, current)
			currentColor = color
			current = tierRun{color: color}
			current.xValues = append(current.xValues, float64(i))
			current.yValues = append(current.yValues, prices[i])
		}
	}
	runs = append(runs, current)

	for i := range runs {
		tier := tierOf(runs[i].color)
		if !named[tier] {
			runs[i].name = legendNames[tier]
			named[tier] = true
		}
	}

	return runs
}

// buildTicks returns x-axis ticks every labelStep hours with Madrid-time labels
func buildTicks(points []PricePoint, labelStep int, madrid *time.Location) []chart.Tick {
	var ticks []chart.Tick
	for i, p := range points {
		if i%labelStep != 0 {
			continue
		}
		label := fmt.Sprintf("%02d:00", p.Datetime.In(madrid).Hour())
		if i == 24 {
			label = "00(+1)"
		}
		ticks = append(ticks, chart.Tick{Value: float64(i), Label: label})
	}
	return ticks
}

// buildAnnotations builds the current-hour and midnight separator annotations
func buildAnnotations(points []PricePoint, data *chartData) []chart.Value2 {
	var annotations []chart.Value2

	var maxPrice float64
	for _, p := range points {
		if p.Value > maxPrice {
			maxPrice = p.Value
		}
	}
	y := maxPrice / 1000

	// Current hour line (only if the current UTC hour is present in today's data)
	now := time.Now().UTC()
	todayStr := now.Format("2006-01-02")
	for i, p := range points {
		pUTC := p.Datetime.UTC()
		if pUTC.Format("2006-01-02") == todayStr && pUTC.Hour() == now.Hour() {
			annotations = append(annotations, chart.Value2{
				XValue: float64(i),
				YValue: y,
				Label:  "Hora actual",
			})
			break
		}
	}

	// Midnight separator between days
	if data.IsDualDay && len(points) > 24 {
		annotations = append(annotations, chart.Value2{
			XValue: 23.5,
			YValue: y,
			Label:  "MEDIANOCHE",
		})
	}

	return annotations
}
