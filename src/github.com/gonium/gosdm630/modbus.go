package sdm630

import (
	"encoding/binary"
	"github.com/goburrow/modbus"
	"log"
	"math"
	"os"
	"time"
)

const (
	MaxRetryCount = 5
)

/***
 * Opcodes as defined by Eastron.
 * See http://bg-etech.de/download/manual/SDM630Register.pdf
 * Please note that this is the superset of all SDM devices - some
 * opcodes might not work on some devices.
 */
const (
	OpCodeL1Voltage   = 0x0000
	OpCodeL2Voltage   = 0x0002
	OpCodeL3Voltage   = 0x0004
	OpCodeL1Current   = 0x0006
	OpCodeL2Current   = 0x0008
	OpCodeL3Current   = 0x000A
	OpCodeL1Power     = 0x000C
	OpCodeL2Power     = 0x000E
	OpCodeL3Power     = 0x0010
	OpCodeL1Import    = 0x015a
	OpCodeL2Import    = 0x015c
	OpCodeL3Import    = 0x015e
	OpCodeTotalImport = 0x0048
	OpCodeL1Export    = 0x0160
	OpCodeL2Export    = 0x0162
	OpCodeL3Export    = 0x0164
	OpCodeTotalExport = 0x004a
	OpCodeL1Cosphi    = 0x001e
	OpCodeL2Cosphi    = 0x0020
	OpCodeL3Cosphi    = 0x0022
	//OpCodeL1THDCurrent         = 0x00F0
	//OpCodeL2THDCurrent         = 0x00F2
	//OpCodeL3THDCurrent         = 0x00F4
	//OpCodeAvgTHDCurrent        = 0x00Fa
	OpCodeL1THDVoltageNeutral  = 0x00ea
	OpCodeL2THDVoltageNeutral  = 0x00ec
	OpCodeL3THDVoltageNeutral  = 0x00ee
	OpCodeAvgTHDVoltageNeutral = 0x00F8
)

type ModbusEngine struct {
	client       modbus.Client
	handler      *modbus.RTUClientHandler
	inputStream  QuerySnipChannel
	outputStream QuerySnipChannel
	devids       []uint8
	verbose      bool
	status       *Status
}

func NewModbusEngine(
	rtuDevice string,
	comset int,
	verbose bool,
	inputChannel QuerySnipChannel,
	outputChannel QuerySnipChannel,
	devids []uint8,
	status *Status,
) *ModbusEngine {
	// Modbus RTU/ASCII
	rtuclient := modbus.NewRTUClientHandler(rtuDevice)
	// TODO: Switch based on comset
	switch comset {
	case 1:
		rtuclient.BaudRate = 2400
	case 2:
		rtuclient.BaudRate = 9600
	case 4:
		rtuclient.BaudRate = 19200
	default:
		log.Fatal("Invalid communication set specified. See -h for help.")
	}
	rtuclient.DataBits = 8
	rtuclient.Parity = "N"
	rtuclient.StopBits = 1
	rtuclient.SlaveId = devids[0]
	rtuclient.Timeout = 1000 * time.Millisecond
	if verbose {
		rtuclient.Logger = log.New(os.Stdout, "RTUClientHandler: ", log.LstdFlags)
		log.Printf("Connecting to RTU via %s, %d %d%s%d\r\n", rtuDevice,
			rtuclient.BaudRate, rtuclient.DataBits, rtuclient.Parity,
			rtuclient.StopBits)
	}

	err := rtuclient.Connect()
	if err != nil {
		log.Fatal("Failed to connect: ", err)
	}
	defer rtuclient.Close()

	mbclient := modbus.NewClient(rtuclient)

	return &ModbusEngine{
		client: mbclient, handler: rtuclient,
		inputStream: inputChannel, outputStream: outputChannel,
		devids: devids, verbose: verbose,
		status: status,
	}
}

func (q *ModbusEngine) retrieveOpCode(opcode uint16) (retval float64,
	err error) {
	q.status.IncreaseModbusRequestCounter()
	results, err := q.client.ReadInputRegisters(opcode, 2)
	if err == nil {
		retval = rtuToFloat64(results)
	} else if q.verbose {
		log.Printf("Failed to retrieve opcode 0x%x, error was: %s\r\n", opcode, err.Error())
	}
	return retval, err
}

func (q *ModbusEngine) queryOrFail(opcode uint16) (retval float64) {
	var err error
	tryCnt := 0
	for tryCnt = 0; tryCnt < MaxRetryCount; tryCnt++ {
		retval, err = q.retrieveOpCode(opcode)
		if err != nil {
			q.status.IncreaseModbusReconnectCounter()
			log.Printf("Failed to retrieve opcode - retry attempt %d of %d\r\n", tryCnt+1,
				MaxRetryCount)
			time.Sleep(time.Duration(100) * time.Millisecond)
		} else {
			break
		}
	}
	if tryCnt == MaxRetryCount {
		log.Fatal("Cannot query the sensor, reached maximum retry count. Did you specify the correct device id and communication parameters?")
	}
	return retval
}

func (q *ModbusEngine) Transform() {
	var previousDeviceId uint8 = 0
	for {
		snip := <-q.inputStream
		q.handler.SlaveId = snip.DeviceId
		// apparently the turnaround timeout must be respected
		// See http://www.modbus.org/docs/Modbus_over_serial_line_V1_02.pdf
		// 3.5 chars at 9600 Baud take 36 ms
		if previousDeviceId != snip.DeviceId {
			time.Sleep(time.Duration(40) * time.Millisecond)
		}
		previousDeviceId = snip.DeviceId
		value := q.queryOrFail(snip.OpCode)
		snip.Value = value
		snip.ReadTimestamp = time.Now()
		q.outputStream <- snip
	}
	// go vet reports this as unreachable (correctly), so
	// just commented out.
	//q.handler.Close()
}

func rtuToFloat64(b []byte) float64 {
	bits := binary.BigEndian.Uint32(b)
	f := math.Float32frombits(bits)
	return float64(f)
}