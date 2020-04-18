package slice

import (
	"GoSlicer/slicer/clip"
	"GoSlicer/slicer/data"
	"GoSlicer/util"
	"fmt"
	clipper "github.com/ctessum/go.clipper"
	"os"
)

type layer struct {
	segments           []*segment
	faceToSegmentIndex map[int]int
	polygons           data.Paths
	closed             []bool
	number             int
}

func newLayer(number int) *layer {
	return &layer{
		faceToSegmentIndex: map[int]int{},
		number:             number,
	}
}

func (l *layer) Polygons() data.Paths {
	return l.polygons
}

func (l *layer) makePolygons(om data.OptimizedModel) {
	// try for each segment to generate a slicePolygon with other segments
	// if the segment is not already assigned to another slicePolygon
	for startSegmentIndex, segment := range l.segments {
		if segment.addedToPolygon {
			continue
		}

		var polygon = data.Path{}
		polygon = append(polygon, l.segments[startSegmentIndex].start)

		currentSegmentIndex := startSegmentIndex
		var canClose bool

		for {
			canClose = false
			currentSegment := l.segments[currentSegmentIndex]
			currentSegment.addedToPolygon = true
			p0 := currentSegment.end
			polygon = append(polygon, p0)

			nextIndex := -1
			// get the whole face for the index
			face := om.OptimizedFace(currentSegment.faceIndex)

			// For each touching face of the current face
			// check if touching face is in this layer.
			// Then calculate the difference between the current end-point (p0)
			// and the starting points of the segments of the touching faces.
			// if it is below the threshold
			// * check if it is the same segment as the starting segment of this round
			//   -> close it as a slicePolygon is finished
			// * if the segment is already added just continue
			// then set the next index to the touching segment
			for _, touchingFaceIndex := range face.TouchingFaceIndices() {
				touchingSegmentIndex, ok := l.faceToSegmentIndex[touchingFaceIndex]
				if touchingFaceIndex > -1 && ok {
					p1 := l.segments[touchingSegmentIndex].start
					diff := p0.Sub(p1)

					if diff.ShorterThan(30) {
						if touchingSegmentIndex == startSegmentIndex {
							canClose = true
						}
						if l.segments[touchingSegmentIndex].addedToPolygon {
							continue
						}
						nextIndex = touchingSegmentIndex
					}
				}
			}

			if nextIndex == -1 {
				break
			}

			currentSegmentIndex = nextIndex
		}

		l.polygons = append(l.polygons, polygon)
		l.closed = append(l.closed, canClose)
	}

	snapDistance := util.Micrometer(100)
	// Connect polygons that are not closed yet.
	// As models are not always perfect manifold we need to join
	// some stuff up to get proper polygons.
RerunConnectPolygons:
	for i, polygon := range l.polygons {
		if polygon == nil || l.closed[i] {
			continue
		}

		best := -1
		bestScore := snapDistance + 1
		for j, polygon2 := range l.polygons {
			if polygon2 == nil || l.closed[j] || i == j {
				continue
			}

			// check the distance of the last point from the first unfinished slicePolygon
			// with the first point of the second unfinished slicePolygon
			diff := polygon[len(polygon)-1].Sub(polygon2[0])
			if diff.ShorterThan(snapDistance) {
				score := diff.Size() - util.Micrometer(len(polygon2)*10)
				if score < bestScore {
					best = j
					bestScore = score
				}
			}
		}

		// if a matching slicePolygon was found, connect them
		if best > -1 {
			for _, aPointFromBest := range l.polygons[best] {
				l.polygons[i] = append(l.polygons[i], aPointFromBest)
			}

			// close slicePolygon if the start end end now fits inside the snap distance
			if polygon.IsAlmostFinished(snapDistance) {
				l.removeLastPoint(i)
				l.closed[i] = true
			}

			// erase the merged slicePolygon
			l.polygons[best] = nil
			// restart search
			goto RerunConnectPolygons
		}
	}

	var clearedPolygons data.Paths
	snapDistance = util.Micrometer(1000)
	for i, poly := range l.polygons {
		if poly == nil {
			continue
		}

		// check if slicePolygon is almost finished
		// if yes just finish it
		if poly.IsAlmostFinished(snapDistance) {
			l.removeLastPoint(i)
			l.closed[i] = true
		}

		// remove tiny polygons or not closed polygons
		length := util.Micrometer(0)
		for n, point := range poly {
			// ignore first point
			if n == 0 {
				continue
			}

			length += point.Sub(poly[n-1]).Size()
			if l.closed[i] && length > snapDistance {
				break
			}
		}

		// remove already cleared polygons and filter also not closed / too small ones
		if l.polygons[i] != nil && length > snapDistance && l.closed[i] {
			clearedPolygons = append(clearedPolygons, l.polygons[i])
		}
	}

	l.polygons = clearedPolygons
}

func (l *layer) gnerateLayerParts() (data.PartitionedLayer, bool) {
	c := clip.NewClip()
	return c.GenerateLayerParts(l)
}

func dumpPolygon(buf *os.File, polygons clipper.Path, modelSize util.MicroVec3, isRed bool) {
	buf.WriteString("<polygon points=\"")
	for _, p := range polygons {
		buf.WriteString(fmt.Sprintf("%f,%f ", (float64(p.X)+float64(modelSize.X())/2)/float64(modelSize.X())*150, (float64(p.Y)+float64(modelSize.Y())/2)/float64(modelSize.Y())*150))

	}
	if isRed {
		buf.WriteString("\" style=\"fill:red; stroke:black;stroke-width:1\" />\n")
	} else {
		buf.WriteString("\" style=\"fill:gray; s1031.680000640troke:black;stroke-width:1\" />\n")
	}
}

func (l *layer) removeLastPoint(polyIndex int) {
	l.polygons[polyIndex] = l.polygons[polyIndex][:len(l.polygons[polyIndex])]
}

/*
func (l *layer) dump(buf *os.File, modelSize util.MicroVec3) {
	buf.WriteString("<svg xmlns=\"http://www.w3.org/2000/svg\" version=\"1.1\" style=\"width: 150px; height:150px\">\n")
	for _, part := range l.parts {
		for j, poly := range part.polygons {
			dumpPolygon(buf, poly, modelSize, j == 0)

		}
	}
	buf.WriteString("</svg>\n")
}


*/
