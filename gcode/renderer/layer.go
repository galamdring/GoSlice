// This file provides renderers for gcode injected at specific layers.

package renderer

import (
	"github.com/aligator/goslice/data"
	"github.com/aligator/goslice/gcode"
)

// PreLayer adds starting gcode, resets the extrude speeds on each layer and enables the fan above a specific layer.
type PreLayer struct{}

func (PreLayer) Init(model data.OptimizedModel) {}

func (PreLayer) Render(b *gcode.Builder, layerNr int, maxLayer int, layer data.PartitionedLayer, z data.Micrometer, options *data.Options) error {
	b.AddComment("LAYER:%v", layerNr)
	if layerNr == 0 {
		b.AddComment("Generated with GoSlice")
		b.AddComment("______________________")

		b.AddCommand("M107 ; disable fan")

		// set and wait for the initial temperature
		b.AddComment("SET_INITIAL_TEMP")
		b.AddCommand("M104 S%d ; start heating hot end", options.Filament.InitialHotEndTemperature)
		b.AddCommand("M190 S%d ; heat and wait for bed", options.Filament.InitialBedTemperature)
		b.AddCommand("M109 S%d ; wait for hot end temperature", options.Filament.InitialHotEndTemperature)

		// starting gcode
		b.AddComment("START_GCODE")
		b.AddCommand("G1 Z5 F5000 ; lift nozzle")
		b.AddCommand("G92 E0 ; reset extrusion distance")

		b.SetExtrusion(options.Print.InitialLayerThickness, options.Printer.ExtrusionWidth)

		// set speeds
		b.SetExtrudeSpeed(options.Print.LayerSpeed)
		b.SetMoveSpeed(options.Print.MoveSpeed)

		// set retraction
		b.SetRetractionSpeed(options.Filament.RetractionSpeed)
		b.SetRetractionAmount(options.Filament.RetractionLength)

		// force the InitialLayerSpeed for first layer
		b.SetExtrudeSpeedOverride(options.Print.IntialLayerSpeed)
	} else if layerNr == 1 {
		b.SetExtrusion(options.Print.LayerThickness, options.Printer.ExtrusionWidth)
	}

	if layerNr > 0 {
		b.DisableExtrudeSpeedOverride()
		b.SetExtrudeSpeed(options.Print.LayerSpeed)
	}

	if fanSpeed, ok := options.Filament.FanSpeed.LayerToSpeedLUT[layerNr]; ok {
		if fanSpeed == 0 {
			b.AddCommand("M107 ; disable fan")
		} else {
			b.AddCommand("M106 S%d; change fan speed", fanSpeed)
		}
	}

	if layerNr == options.Filament.InitialTemperatureLayerCount {
		// set the normal temperature
		// this is done without waiting
		b.AddComment("SET_TEMP")
		b.AddCommand("M140 S%d", options.Filament.BedTemperature)
		b.AddCommand("M104 S%d", options.Filament.HotEndTemperature)
	}

	return nil
}

// PostLayer adds GCode at the last layer.
type PostLayer struct{}

func (PostLayer) Init(model data.OptimizedModel) {}

func (PostLayer) Render(b *gcode.Builder, layerNr int, maxLayer int, layer data.PartitionedLayer, z data.Micrometer, options *data.Options) error {
	// ending gcode
	if layerNr == maxLayer {
		b.AddComment("END_GCODE")
		b.SetExtrusion(options.Print.LayerThickness, options.Printer.ExtrusionWidth)
		b.AddCommand("M107 ; disable fan")

		// disable heaters
		b.AddCommand("M104 S0 ; Set Hot-end to 0C (off)")
		b.AddCommand("M140 S0 ; Set bed to 0C (off)")

		b.AddCommand("G28 X0  ; home X axis to get head out of the way")
		b.AddCommand("M84 ;steppers off")

	}

	return nil
}
