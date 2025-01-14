package writer

import (
	"github.com/aligator/goslice/handler"
	"os"
)

type writer struct{}

// Writer can write gcode to a file.
func Writer() handler.GCodeWriter {
	return &writer{}
}

func (w writer) Write(gcode string, filename string) error {
	buf, err := os.Create(filename)
	if err != nil {
		return err
	}

	defer buf.Close()

	_, err = buf.WriteString(gcode)
	return err
}
