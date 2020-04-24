// Package clip provides an implementation for clipping polygons.
// Currently the only implementation is using the github.com/aligator/go.clipper library.
package clip

import (
	"GoSlice/data"
	"fmt"
	clipper "github.com/aligator/go.clipper"
)

// Clipper is an interface that provides methods needed by GoSlice to clip polygons.
type Clipper interface {
	// GenerateLayerParts partitions the whole layer into several partition parts.
	GenerateLayerParts(l data.Layer) (data.PartitionedLayer, bool)

	// InsetLayer returns all new paths generated by insetting all parts of the layer.
	// The result is built the following way: [part][insetNr][insetParts]data.LayerPart
	//
	//  * Part is the part in the from the input-layer.
	//  * Wall is the wall of the part. The first wall is the outer perimeter.
	//  * InsetNum is the number of the inset (starting by the outer walls with 0)
	//    and all following are from holes inside of the polygon.
	// The array for a part may be empty.
	InsetLayer(layer []data.LayerPart, offset data.Micrometer, insetCount int) [][][]data.LayerPart

	// Inset insets the given layer part.
	// The result is built the following way: [insetNr][insetParts]data.LayerPart
	//
	//  * Wall is the wall of the part. The first wall is the outer perimeter
	//  * InsetNum is the number of the inset (starting by the outer walls with 0)
	//    and all following are from holes inside of the polygon.
	// The array for a part may be empty.
	Inset(part data.LayerPart, offset data.Micrometer, insetCount int) [][]data.LayerPart

	// Fill creates an infill pattern for the given paths.
	// The parameter overlapPercentage should normally be a value between 0 and 100.
	// But it can also be smaller or greater than that if needed.
	// The generated infill will overlap the paths by the percentage of this param.
	// LineWidth is used for both, the calculation of the overlap and the calculation between the lines.
	Fill(paths data.LayerPart, lineWidth data.Micrometer, overlapPercentage int) data.Paths
}

// clipperClipper implements Clipper using the external clipper library.
type clipperClipper struct {
}

// NewClipper returns a new instance of a polygon Clipper.
func NewClipper() Clipper {
	return clipperClipper{}
}

// clipperPoint converts the GoSlice point representation to the
// representation which is used by the external clipper lib.
func clipperPoint(p data.MicroPoint) *clipper.IntPoint {
	return &clipper.IntPoint{
		X: clipper.CInt(p.X()),
		Y: clipper.CInt(p.Y()),
	}
}

// clipperPaths converts the GoSlice Paths representation
// to the representation which is used by the external clipper lib.
func clipperPaths(p data.Paths) clipper.Paths {
	var result clipper.Paths
	for _, path := range p {
		result = append(result, clipperPath(path))
	}

	return result
}

// clipperPath converts the GoSlice Path representation
// to the representation which is used by the external clipper lib.
func clipperPath(p data.Path) clipper.Path {
	var result clipper.Path
	for _, point := range p {
		result = append(result, clipperPoint(point))
	}

	return result
}

// microPoint converts the external clipper lib representation of a point
// to the representation which is used by GoSlice.
func microPoint(p *clipper.IntPoint) data.MicroPoint {
	return data.NewMicroPoint(data.Micrometer(p.X), data.Micrometer(p.Y))
}

// microPath converts the external clipper lib representation of a path
// to the representation which is used by GoSlice.
// The parameter simplify enables simplifying of the path using
// the default simplification settings.
func microPath(p clipper.Path, simplify bool) data.Path {
	var result data.Path
	for _, point := range p {
		result = append(result, microPoint(point))
	}

	if simplify {
		return result.Simplify(-1, -1)
	}
	return result
}

// microPaths converts the external clipper lib representation of paths
// to the representation which is used by GoSlice.
// The parameter simplify enables simplifying of the paths using
// the default simplification settings.
func microPaths(p clipper.Paths, simplify bool) data.Paths {
	var result data.Paths
	for _, path := range p {
		result = append(result, microPath(path, simplify))
	}
	return result
}

func (c clipperClipper) GenerateLayerParts(l data.Layer) (data.PartitionedLayer, bool) {
	polyList := clipper.Paths{}
	// convert all polygons to clipper polygons
	for _, layerPolygon := range l.Polygons() {
		var path = clipper.Path{}

		prev := 0
		// convert all points of this polygons
		for j, layerPoint := range layerPolygon {
			// ignore first as the next check would fail otherwise
			if j == 0 {
				path = append(path, clipperPoint(layerPolygon[0]))
				continue
			}

			// filter too near points
			// check this always with the previous point
			if layerPoint.Sub(layerPolygon[prev]).ShorterThanOrEqual(100) {
				continue
			}

			path = append(path, clipperPoint(layerPoint))
			prev = j
		}

		polyList = append(polyList, path)
	}

	if len(polyList) == 0 {
		return data.NewPartitionedLayer([]data.LayerPart{}), true
	}

	clip := clipper.NewClipper(clipper.IoNone)
	clip.AddPaths(polyList, clipper.PtSubject, true)
	resultPolys, ok := clip.Execute2(clipper.CtUnion, clipper.PftEvenOdd, clipper.PftEvenOdd)
	if !ok {
		return nil, false
	}

	return data.NewPartitionedLayer(c.polyTreeToLayerParts(resultPolys)), true
}

func (c clipperClipper) polyTreeToLayerParts(tree *clipper.PolyTree) []data.LayerPart {
	var layerParts []data.LayerPart

	var polysForNextRound []*clipper.PolyNode

	for _, c := range tree.Childs() {
		polysForNextRound = append(polysForNextRound, c)
	}
	for {
		if polysForNextRound == nil {
			break
		}
		thisRound := polysForNextRound
		polysForNextRound = nil

		for _, p := range thisRound {
			var holes data.Paths

			for _, child := range p.Childs() {
				// TODO: simplyfy, yes / no ??
				holes = append(holes, microPath(child.Contour(), false))
				for _, c := range child.Childs() {
					polysForNextRound = append(polysForNextRound, c)
				}
			}

			// TODO: simplify, yes / no ??
			layerParts = append(layerParts, data.NewUnknownLayerPart(microPath(p.Contour(), false), holes))
		}
	}

	return layerParts
}

func (c clipperClipper) InsetLayer(layer []data.LayerPart, offset data.Micrometer, insetCount int) [][][]data.LayerPart {
	var result [][][]data.LayerPart
	for _, part := range layer {
		result = append(result, c.Inset(part, offset, insetCount))
	}

	return result
}

func (c clipperClipper) Inset(part data.LayerPart, offset data.Micrometer, insetCount int) [][]data.LayerPart {
	var insets [][]data.LayerPart

	o := clipper.NewClipperOffset()

	for insetNr := 0; insetNr < insetCount; insetNr++ {
		// insets for the outline
		o.Clear()
		o.AddPaths(clipperPaths(data.Paths{part.Outline()}), clipper.JtSquare, clipper.EtClosedPolygon)
		o.AddPaths(clipperPaths(part.Holes()), clipper.JtSquare, clipper.EtClosedPolygon)

		o.MiterLimit = 2
		allNewInsets := o.Execute2(float64(-int(offset)*insetNr) - float64(offset/2))
		insets = append(insets, c.polyTreeToLayerParts(allNewInsets))
	}

	return insets
}

func (c clipperClipper) Fill(paths data.LayerPart, lineWidth data.Micrometer, overlapPercentage int) data.Paths {
	min, max := paths.Outline().Size()
	cPath := clipperPath(paths.Outline())
	cHoles := clipperPaths(paths.Holes())
	result := c.getLinearFill(cPath, cHoles, min, max, lineWidth, overlapPercentage)

	return microPaths(result, false)
}

// getLinearFill provides a infill which uses simple parallel lines
func (c clipperClipper) getLinearFill(outline clipper.Path, holes clipper.Paths, minScanlines data.MicroPoint, maxScanlines data.MicroPoint, lineWidth data.Micrometer, overlapPercentage int) clipper.Paths {
	cl := clipper.NewClipper(clipper.IoNone)
	co := clipper.NewClipperOffset()
	var result clipper.Paths

	overlap := float32(lineWidth) * (100.0 - float32(overlapPercentage)) / 100.0

	lines := clipper.Paths{}
	numLine := 0
	// generate the lines
	for x := minScanlines.X(); x <= maxScanlines.X(); x += lineWidth {
		// switch line direction based on even / odd
		if numLine%2 == 1 {
			lines = append(lines, clipper.Path{
				&clipper.IntPoint{
					X: clipper.CInt(x),
					Y: clipper.CInt(maxScanlines.Y()),
				},
				&clipper.IntPoint{
					X: clipper.CInt(x),
					Y: clipper.CInt(minScanlines.Y()),
				},
			})
		} else {
			lines = append(lines, clipper.Path{
				&clipper.IntPoint{
					X: clipper.CInt(x),
					Y: clipper.CInt(minScanlines.Y()),
				},
				&clipper.IntPoint{
					X: clipper.CInt(x),
					Y: clipper.CInt(maxScanlines.Y()),
				},
			})
		}
		numLine++
	}

	// clip the paths with the lines using intersection
	inset := clipper.Paths{outline}

	// generate the inset for the overlap (only if needed)
	if overlapPercentage != 0 {
		co.AddPaths(inset, clipper.JtSquare, clipper.EtClosedPolygon)
		co.MiterLimit = 2
		inset = co.Execute(float64(-overlap))
	}

	// clip the lines by the resulting inset
	cl.AddPaths(inset, clipper.PtClip, true)
	cl.AddPaths(holes, clipper.PtClip, true)
	cl.AddPaths(lines, clipper.PtSubject, false)

	tree, ok := cl.Execute2(clipper.CtIntersection, clipper.PftEvenOdd, clipper.PftEvenOdd)
	if !ok {
		fmt.Println("getLinearFill failed")
		return nil
	}

	for _, c := range tree.Childs() {
		result = append(result, c.Contour())
	}

	cl.Clear()
	co.Clear()

	return result
}
