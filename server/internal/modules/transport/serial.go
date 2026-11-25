// Package transport opens connections to modules. Serial (USB-CDC) for real modules;
// the virtual transport for the simulator follows in task 6.
package transport

import (
	"io"
	"runtime"
	"sort"
	"strings"

	"go.bug.st/serial"
	"go.bug.st/serial/enumerator"
)

// PortInfo describes one serial port.
type PortInfo struct {
	Name    string
	USB     bool
	VID     string // hex, e.g. "2E8A"
	PID     string
	Serial  string
	Product string
}

// ListSerial returns the serial ports of this machine. On macOS only the /dev/cu.* variant
// of each port is listed (the /dev/tty.* one blocks on open until carrier detect).
func ListSerial() ([]PortInfo, error) {
	details, err := enumerator.GetDetailedPortsList()
	if err != nil {
		return nil, err
	}
	var out []PortInfo
	for _, d := range details {
		if runtime.GOOS == "darwin" && strings.HasPrefix(d.Name, "/dev/tty.") {
			continue
		}
		out = append(out, PortInfo{
			Name: d.Name, USB: d.IsUSB,
			VID: strings.ToUpper(d.VID), PID: strings.ToUpper(d.PID),
			Serial: d.SerialNumber, Product: d.Product,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// OpenSerial opens a module port at 115200 baud, 8N1 (spec 01 §2).
// DTR is set and RTS cleared on purpose: some boards reset into their bootloader
// when these lines toggle (D7), and some USB-CDC stacks only send while DTR is set.
func OpenSerial(name string) (io.ReadWriteCloser, error) {
	return serial.Open(name, &serial.Mode{
		BaudRate: 115200,
		DataBits: 8,
		Parity:   serial.NoParity,
		StopBits: serial.OneStopBit,
		InitialStatusBits: &serial.ModemOutputBits{
			DTR: true,
			RTS: false,
		},
	})
}

// KnownVendors maps USB vendor ids that are probed automatically to a name (D7).
// Other ports are only probed when the user asks for it.
var KnownVendors = map[string]string{
	"2E8A": "Raspberry Pi",
	"303A": "Espressif",
	"2341": "Arduino",
	"239A": "Adafruit",
	"2886": "Seeed",
	"1B4F": "SparkFun",
	"0483": "STMicroelectronics",
	"1A86": "WCH (CH340)",
	"10C4": "Silicon Labs (CP210x)",
	"0403": "FTDI",
}
