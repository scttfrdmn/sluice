// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Scott Friedman

package report

import (
	"image/color"

	"gonum.org/v1/plot"
	"gonum.org/v1/plot/plotter"
	"gonum.org/v1/plot/vg"
	"gonum.org/v1/plot/vg/draw"
)

// PlotAccuracyCost draws pooled accuracy against mean cost per call for
// each model, plus the best-per-class policy and the oracle.
func (r *Report) PlotAccuracyCost(path string) error {
	h := r.Groups["pooled"]
	p := plot.New()
	p.Title.Text = "E0: accuracy (both draws) vs mean cost per call"
	p.X.Label.Text = "mean USD per call"
	p.Y.Label.Text = "accuracy"
	p.Y.Min, p.Y.Max = 0, 1
	cost := map[string]float64{}
	for _, m := range r.CostLatency {
		cost[m.Model] = m.MeanUSDPerCall
	}
	var pts plotter.XYs
	var labels []string
	for _, m := range r.Models {
		pts = append(pts, plotter.XY{X: cost[m.Name], Y: h.Accuracy[m.Name]})
		labels = append(labels, m.Name)
	}
	sc, err := plotter.NewScatter(pts)
	if err != nil {
		return err
	}
	sc.GlyphStyle.Shape = draw.CircleGlyph{}
	sc.GlyphStyle.Color = color.RGBA{R: 60, G: 90, B: 160, A: 255}
	sc.GlyphStyle.Radius = vg.Points(4)
	lb, err := plotter.NewLabels(plotter.XYLabels{XYs: pts, Labels: labels})
	if err != nil {
		return err
	}
	p.Add(sc, lb)
	if r.Policy.Leaves > 0 {
		pc := r.Policy.BestPerClassUSD / float64(r.Policy.Leaves)
		extra := plotter.XYs{{X: pc, Y: h.BestPerClass}}
		ex, _ := plotter.NewScatter(extra)
		ex.GlyphStyle.Shape = draw.BoxGlyph{}
		ex.GlyphStyle.Color = color.RGBA{R: 200, G: 110, B: 30, A: 255}
		ex.GlyphStyle.Radius = vg.Points(4)
		exl, _ := plotter.NewLabels(plotter.XYLabels{XYs: extra, Labels: []string{"best-per-class"}})
		p.Add(ex, exl)
	}
	// The oracle has no single cost; it is drawn as a horizontal line.
	ol, err := plotter.NewLine(plotter.XYs{{X: 0, Y: h.Oracle}, {X: maxCost(cost) * 1.1, Y: h.Oracle}})
	if err != nil {
		return err
	}
	ol.LineStyle.Dashes = []vg.Length{vg.Points(4), vg.Points(3)}
	ol.LineStyle.Color = color.Gray{Y: 110}
	p.Add(ol)
	p.Legend.Add("oracle", ol)
	p.Legend.Top = true
	return p.Save(7*vg.Inch, 5*vg.Inch, path)
}

func maxCost(m map[string]float64) float64 {
	var x float64
	for _, v := range m {
		x = max(x, v)
	}
	if x == 0 {
		return 1
	}
	return x
}

// PlotClassRho draws per-class ρ as bars (undefined classes omitted).
func (r *Report) PlotClassRho(path string) error {
	p := plot.New()
	p.Title.Text = "E0: class-oracle fraction ρ_c by class"
	p.Y.Label.Text = "ρ_c"
	var vals plotter.Values
	var names []string
	for _, c := range r.Classes {
		if v := r.ClassRho[c.Class]; v != nil {
			vals = append(vals, *v)
			names = append(names, c.Class)
		}
	}
	if len(vals) == 0 {
		vals, names = plotter.Values{0}, []string{"(none defined)"}
	}
	bars, err := plotter.NewBarChart(vals, vg.Points(14))
	if err != nil {
		return err
	}
	bars.Color = color.RGBA{R: 60, G: 90, B: 160, A: 255}
	bars.LineStyle.Width = 0
	p.Add(bars)
	p.NominalX(names...)
	p.X.Tick.Label.Rotation = 1.1
	p.X.Tick.Label.XAlign = draw.XRight
	p.X.Tick.Label.YAlign = draw.YCenter
	ref, _ := plotter.NewLine(plotter.XYs{{X: -0.5, Y: 0.5}, {X: float64(len(vals)) - 0.5, Y: 0.5}})
	ref.LineStyle.Dashes = []vg.Length{vg.Points(4), vg.Points(3)}
	ref.LineStyle.Color = color.Gray{Y: 110}
	p.Add(ref)
	w := vg.Length(max(6, len(vals))) * 0.45 * vg.Inch
	return p.Save(w, 5*vg.Inch, path)
}
