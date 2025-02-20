package goslice

import (
	"github.com/aligator/goslice/clip"
	"github.com/aligator/goslice/data"
	"github.com/aligator/goslice/gcode"
	"github.com/aligator/goslice/gcode/renderer"
	"github.com/aligator/goslice/handler"
	"github.com/aligator/goslice/modifier"
	"github.com/aligator/goslice/optimizer"
	"github.com/aligator/goslice/reader"
	"github.com/aligator/goslice/slicer"
	"github.com/aligator/goslice/writer"
	"time"
)

// GoSlice combines all logic  needed to slice
// a model and generate a GCode file.
type GoSlice struct {
	Options   data.GoSliceOptions
	Reader    handler.ModelReader
	Optimizer handler.ModelOptimizer
	Slicer    handler.ModelSlicer
	Modifiers []handler.LayerModifier
	Generator handler.GCodeGenerator
	Writer    handler.GCodeWriter
}

// NewGoSlice provides a GoSlice with all built in implementations.
func NewGoSlice(options data.Options) *GoSlice {
	s := &GoSlice{
		Options: options.GoSlice,
	}

	// create handlers
	topBottomPatternFactory := func(min data.MicroPoint, max data.MicroPoint) clip.Pattern {
		return clip.NewLinearPattern(options.Printer.ExtrusionWidth, options.Printer.ExtrusionWidth, min, max, options.Print.InfillRotationDegree, true, false)
	}

	s.Reader = reader.Reader(&options)
	s.Optimizer = optimizer.NewOptimizer(&options)
	s.Slicer = slicer.NewSlicer(&options)
	s.Modifiers = []handler.LayerModifier{
		modifier.NewPerimeterModifier(&options),
		modifier.NewInfillModifier(&options),
		modifier.NewInternalInfillModifier(&options),
		modifier.NewBrimModifier(&options),
		modifier.NewSupportDetectorModifier(&options),
		modifier.NewSupportGeneratorModifier(&options),
	}

	patternSpacing := options.Print.Support.PatternSpacing.ToMicrometer()

	s.Generator = gcode.NewGenerator(
		&options,
		gcode.WithRenderer(renderer.PreLayer{}),
		gcode.WithRenderer(renderer.Skirt{}),
		gcode.WithRenderer(renderer.Brim{}),
		gcode.WithRenderer(renderer.Perimeter{}),

		// Add infill for support generation.
		gcode.WithRenderer(&renderer.Infill{
			PatternSetup: func(min data.MicroPoint, max data.MicroPoint) clip.Pattern {
				// make bounding box bigger to allow generation of support which has always at least two lines
				min.SetX(min.X() - patternSpacing)
				min.SetY(min.Y() - patternSpacing)
				max.SetX(max.X() + patternSpacing)
				max.SetY(max.Y() + patternSpacing)
				return clip.NewLinearPattern(options.Printer.ExtrusionWidth, patternSpacing, min, max, 90, false, true)
			},
			AttrName: "support",
			Comments: []string{"TYPE:SUPPORT"},
		}),
		// Interface pattern for support generation is generated by rotating 90° to the support and no spaces between the lines.
		gcode.WithRenderer(&renderer.Infill{
			PatternSetup: func(min data.MicroPoint, max data.MicroPoint) clip.Pattern {
				// make bounding box bigger to allow generation of support which has always at least two lines
				min.SetX(min.X() - patternSpacing)
				min.SetY(min.Y() - patternSpacing)
				max.SetX(max.X() + patternSpacing)
				max.SetY(max.Y() + patternSpacing)
				return clip.NewLinearPattern(options.Printer.ExtrusionWidth, options.Printer.ExtrusionWidth, min, max, 0, false, true)
			},
			AttrName: "supportInterface",
			Comments: []string{"TYPE:SUPPORT"},
		}),

		gcode.WithRenderer(&renderer.Infill{
			PatternSetup: topBottomPatternFactory,
			AttrName:     "bottom",
			Comments:     []string{"TYPE:FILL", "BOTTOM-FILL"},
		}),
		gcode.WithRenderer(&renderer.Infill{
			PatternSetup: topBottomPatternFactory,
			AttrName:     "top",
			Comments:     []string{"TYPE:FILL", "TOP-FILL"},
		}),
		gcode.WithRenderer(&renderer.Infill{
			PatternSetup: func(min data.MicroPoint, max data.MicroPoint) clip.Pattern {
				// TODO: the calculation of the percentage is currently very basic and may not be correct.

				if options.Print.InfillPercent != 0 {
					mm10 := data.Millimeter(10).ToMicrometer()
					linesPer10mmFor100Percent := mm10 / options.Printer.ExtrusionWidth
					linesPer10mmForInfillPercent := float64(linesPer10mmFor100Percent) * float64(options.Print.InfillPercent) / 100.0

					lineWidth := data.Micrometer(float64(mm10) / linesPer10mmForInfillPercent)

					return clip.NewLinearPattern(options.Printer.ExtrusionWidth, lineWidth, min, max, options.Print.InfillRotationDegree, true, options.Print.InfillZigZag)
				}

				return nil
			},
			AttrName: "infill",
			Comments: []string{"TYPE:FILL", "INTERNAL-FILL"},
		}),
		gcode.WithRenderer(renderer.PostLayer{}),
	)
	s.Writer = writer.Writer()

	return s
}

func (s *GoSlice) Process() error {
	startTime := time.Now()

	// 1. Load model
	s.Options.Logger.Printf("Load model %v\n", s.Options.InputFilePath)
	models, err := s.Reader.Read(s.Options.InputFilePath)
	if err != nil {
		return err
	}
	s.Options.Logger.Printf("Model loaded.\nFace count: %v\nSize: min: %v max %v\n", models.FaceCount(), models.Min(), models.Max())

	// 2. Optimize model
	var optimizedModel data.OptimizedModel
	optimizedModel, err = s.Optimizer.Optimize(models)
	if err != nil {
		return err
	}
	s.Options.Logger.Printf("Model optimized\n")

	//err = optimizedModel.SaveDebugSTL("test.stl")
	//if err != nil {
	//	return err
	//}

	// 3. Slice model into layers
	layers, err := s.Slicer.Slice(optimizedModel)
	if err != nil {
		return err
	}
	s.Options.Logger.Printf("Model sliced to %v layers\n", len(layers))

	// 4. Modify the layers
	// e.g. generate perimeter paths,
	// generate the parts which should be filled in, ...
	for _, m := range s.Modifiers {
		m.Init(optimizedModel)
		err = m.Modify(layers)
		if err != nil {
			return err
		}
		s.Options.Logger.Printf("Modifier %s applied\n", m.GetName())
	}
	s.Options.Logger.Printf("Layers modified %v\n", len(layers))

	// 5. generate gcode from the layers
	s.Generator.Init(optimizedModel)
	finalGcode, err := s.Generator.Generate(layers)
	if err != nil {
		return err
	}

	outputPath := s.Options.OutputFilePath
	if outputPath == "" {
		outputPath = s.Options.InputFilePath + ".gcode"
	}

	err = s.Writer.Write(finalGcode, outputPath)
	s.Options.Logger.Println("full processing time:", time.Now().Sub(startTime))

	return err
}
