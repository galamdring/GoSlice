// This file provides modifiers needed to generate support.
// It contains one supportDetectorModifier and an supportGenerationModifier which is meant to run after the detector,
// so that it can use the information of all layers at once.

package modifier

import (
	"errors"
	"fmt"
	"github.com/aligator/goslice/clip"
	"github.com/aligator/goslice/data"
	"github.com/aligator/goslice/handler"
	"math"
)

// FullSupport extracts the attribute "fullSupport" from the layer.
// If it has the wrong type, a error is returned.
// If it doesn't exist, (nil, nil) is returned.
// If it exists, the support areas are returned.
func FullSupport(layer data.PartitionedLayer) ([]data.LayerPart, error) {
	if attr, ok := layer.Attributes()["fullSupport"]; ok {
		fullSupport, ok := attr.([]data.LayerPart)
		if !ok {
			return nil, errors.New("the attribute fullSupport has the wrong datatype")
		}

		return fullSupport, nil
	}

	return nil, nil
}

type supportDetectorModifier struct {
	handler.Named
	options *data.Options
}

func (m supportDetectorModifier) Init(_ data.OptimizedModel) {}

// NewSupportDetectorModifier calculates the areas which need support.
// It saves them as the attribute "support" as []data.LayerPart.
// It is meant as a preprocessing modifier.
// Another modifier can use this information to generate the actual support.
//
// How it basically works:
// ### = the model
//
// ############
// ############
// ### ___d____  |
// ### |     /   |
// ### |    /    |
// ### h   /     | h = 1 layer height
// ### |  /      |
// ### |θ/       |
// ### |/        |
//
// d = h * tan θ
// https://tams.informatik.uni-hamburg.de/publications/2018/MSc_Daniel_Ahlers.pdf
// 4.1.5  Support Generation
//
// "To get the actual areas where the support is later generated,
//  the previous layer is offset by the calculated d and then subtracted from the current layer.
//  All areas that remain have a higher angle than the threshold and need to be supported."
func NewSupportDetectorModifier(options *data.Options) handler.LayerModifier {
	return &supportDetectorModifier{
		Named: handler.Named{
			Name: "SupportDetector",
		},
		options: options,
	}
}

func (m supportDetectorModifier) Modify(layers []data.PartitionedLayer) error {
	for layerNr := range layers {
		if !m.options.Print.Support.Enabled {
			return nil
		}

		// Ignore top layer to avoid index out of bounds
		// and also ignore the most bottom layers based on the
		// TopGapLayers value because the result is set to layerNr - TopGapLayers.
		if layerNr == len(layers)-1 || layerNr < m.options.Print.Support.TopGapLayers {
			continue
		}

		// calculate distance (d):
		distance := float64(m.options.Print.LayerThickness) * math.Tan(data.ToRadians(float64(m.options.Print.Support.ThresholdAngle)))

		// offset layer by d
		cl := clip.NewClipper()
		offsetLayer := cl.InsetLayer(layers[layerNr].LayerParts(), data.Micrometer(-math.Round(distance)), 1, -data.Micrometer(-math.Round(distance))/2).ToOneDimension()

		// subtract result from the next layer
		support, ok := cl.Difference(layers[layerNr+1].LayerParts(), offsetLayer)
		if !ok {
			return errors.New("could not calculate the support parts")
		}

		// make the support a little bit bigger to provide at least two lines on most places
		support = cl.InsetLayer(support, -m.options.Print.Support.PatternSpacing.ToMicrometer()*3, 1, m.options.Print.Support.PatternSpacing.ToMicrometer()*3/2).ToOneDimension()

		// Save the result at the current layer minus TopGapLayers to skip the amount of TopGapLayers
		newLayer := newExtendedLayer(layers[layerNr-m.options.Print.Support.TopGapLayers])
		if len(support) > 0 {
			newLayer.attributes["support"] = support
		}
		layers[layerNr-m.options.Print.Support.TopGapLayers] = newLayer
	}

	return nil
}

type supportGeneratorModifier struct {
	handler.Named
	options *data.Options
}

func (m supportGeneratorModifier) Init(_ data.OptimizedModel) {}

// NewSupportGeneratorModifier generates the actual areas for the support out of the areas which need support.
// It grows these areas down till the first layer or till it touches the model.
// It also generates the interface parts (the most top support layers which are filled differently)
// and removes them from the normal support areas.
func NewSupportGeneratorModifier(options *data.Options) handler.LayerModifier {
	return &supportGeneratorModifier{
		Named: handler.Named{
			Name: "SupportGenerator",
		},
		options: options,
	}
}

func (m supportGeneratorModifier) Modify(layers []data.PartitionedLayer) error {
	var lastSupport []data.LayerPart = nil

	// for each layer starting at the 2nd top layer (the top layer won't need support)
	for layerNr := len(layers) - 2; layerNr >= 0; layerNr-- {
		if !m.options.Print.Support.Enabled || layerNr == 0 {
			return nil
		}

		// use the result from the last round or if it doesn't exist, load the current layer-support
		currentSupport := lastSupport
		if currentSupport == nil {
			var err error
			currentSupport, err = PartsAttribute(layers[layerNr], "support")
			if err != nil {
				return err
			}
		}

		// load support needed for the layer below
		belowSupport, err := PartsAttribute(layers[layerNr-1], "support")
		if err != nil {
			return err
		}

		if len(currentSupport) == 0 && len(belowSupport) == 0 {
			continue
		}

		cl := clip.NewClipper()

		// union them
		result, ok := cl.Union(currentSupport, belowSupport)
		if !ok {
			return fmt.Errorf("could not union the supports for layer %d to generate support", layerNr)
		}

		// make the layer a bit bigger to create a gap between the support and the model
		biggerLayer := cl.InsetLayer(layers[layerNr-1].LayerParts(), -m.options.Print.Support.Gap.ToMicrometer(), 1, m.options.Print.Support.Gap.ToMicrometer()/2).ToOneDimension()

		// subtract the model from the result
		actualSupport, ok := cl.Difference(result, biggerLayer)
		if !ok {
			return fmt.Errorf("could not subtract the model from the supports for layer %d", layerNr)
		}

		var interfaceParts []data.LayerPart
		var actualWithoutInterfaceParts []data.LayerPart

		if actualSupport != nil {

			// Get the top support parts to calculate the areas where the support-interface pattern should be generated.
			// It takes into account the configuration for the amount of interface layers.
			layerNrAboveInterface := layerNr + m.options.Print.Support.InterfaceLayers - 1 // -1 because we always calculate the support for the layer below
			if layerNrAboveInterface >= len(layers) {
				layerNrAboveInterface = len(layers) - 1
			}

			c := clip.NewClipper()

			supportAboveInterface, err := PartsAttribute(layers[layerNrAboveInterface], "fullSupport")
			if err != nil {
				return err
			}

			interfaceParts, ok = c.Difference(actualSupport, supportAboveInterface)
			if !ok {
				return errors.New("error while calculating interface parts")
			}

			actualWithoutInterfaceParts, ok = c.Difference(actualSupport, interfaceParts)
			if !ok {
				return errors.New("error while calculating the actual support without the interface parts")
			}

			// If there is any brim in this layer, remove it from the support to avoid overlapping.
			brimArea, err := BrimOuterDimension(layers[layerNr-1])
			if err != nil {
				return err
			}
			if brimArea != nil {
				interfaceParts, ok = c.Difference(interfaceParts, brimArea)
				actualWithoutInterfaceParts, ok = c.Difference(actualWithoutInterfaceParts, brimArea)
			}
		}

		lastSupport = actualSupport

		// save the support as actual support to render for the layer below
		newLayer := newExtendedLayer(layers[layerNr-1])
		if len(actualSupport) > 0 {
			// this attribute is not used for rendering but instead for calculating the interface parts for the next layers.
			newLayer.attributes["fullSupport"] = actualSupport
		}
		if len(interfaceParts) > 0 {
			newLayer.attributes["supportInterface"] = interfaceParts
		}

		if len(actualWithoutInterfaceParts) > 0 {
			// replace support from the detection modifier
			newLayer.attributes["support"] = actualWithoutInterfaceParts
		} else {
			// remove maybe existing support from the detection modifier
			newLayer.attributes["support"] = []data.LayerPart{}
		}
		layers[layerNr-1] = newLayer
	}
	return nil
}
