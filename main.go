package main

// typedef unsigned char Uint8;
// void SineWave(void *userdata, Uint8 *stream, int len);
import "C"
import "github.com/veandco/go-sdl2/sdl"
import (
	"bufio"
	"fmt"
	"io/ioutil"
	"math"
	"math/rand"
	"os"
	"reflect"
	"time"
	"unsafe"
)

func check(err error) {
	if err != nil {
		panic(err)
	}
}

var (
	pc            uint16
	indexRegister uint16
	delayTimer    uint8
	soundTimer    uint8
	registers     [16]byte
	display       [64][32]bool // 64 x 32 -- TODO: make this a 2D bit array?
	memory        [4096]byte
	stack         [4096]uint16 // probably don't need the stack this large...
	stackIndex    int

	fontOffset    uint16 = 0x050
	programOffset uint16 = 0x200

	cosmacShift             = false
	superchipJumpWithOffset = true

	running     = true
	debug       = false
	stepThrough = false

	loopSpeed = 11 // instructions processed per loop
	fps       = 60 // should this be ms per loop?

	window          *sdl.Window
	surface         *sdl.Surface
	audioDeviceID   sdl.AudioDeviceID
	keyboard        []uint8
	keypadStates    [16]KeypadState
	keypadStateInit bool

	byteToScancode = map[byte]sdl.Scancode{
		0x0: sdl.SCANCODE_X,
		0x1: sdl.SCANCODE_1,
		0x2: sdl.SCANCODE_2,
		0x3: sdl.SCANCODE_3,
		0x4: sdl.SCANCODE_Q,
		0x5: sdl.SCANCODE_W,
		0x6: sdl.SCANCODE_E,
		0x7: sdl.SCANCODE_A,
		0x8: sdl.SCANCODE_S,
		0x9: sdl.SCANCODE_D,
		0xA: sdl.SCANCODE_Z,
		0xB: sdl.SCANCODE_C,
		0xC: sdl.SCANCODE_4,
		0xD: sdl.SCANCODE_R,
		0xE: sdl.SCANCODE_F,
		0xF: sdl.SCANCODE_V,
	}
	scancodeToByte map[sdl.Scancode]byte
)

func pop() uint16 {
	if stackIndex <= 0 {
		panic("empty stack pop")
	}

	stackIndex--
	return stack[stackIndex]
}

func push(val uint16) {
	stack[stackIndex] = val
	stackIndex++
}

func init() {
	setupSDL()

	// fill reverse map for keyboard events
	scancodeToByte = make(map[sdl.Scancode]byte)
	for k, v := range byteToScancode {
		scancodeToByte[v] = k
	}

	// TODO: get program name externally
	// get program
	//progBytes, err := ioutil.ReadFile("IBM Logo.ch8")
	//progBytes, err := ioutil.ReadFile("test_opcode.ch8")
	//progBytes, err := ioutil.ReadFile("chip8-test-suite.ch8")
	//progBytes, err := ioutil.ReadFile("1dcell.ch8")
	progBytes, err := ioutil.ReadFile("snake.ch8")
	check(err)

	// load program
	var i uint16
	for i = 0; i < uint16(len(progBytes)); i++ {
		memory[programOffset+i] = progBytes[i]
	}

	font := []byte{
		0xF0, 0x90, 0x90, 0x90, 0xF0, // 0
		0x20, 0x60, 0x20, 0x20, 0x70, // 1
		0xF0, 0x10, 0xF0, 0x80, 0xF0, // 2
		0xF0, 0x10, 0xF0, 0x10, 0xF0, // 3
		0x90, 0x90, 0xF0, 0x10, 0x10, // 4
		0xF0, 0x80, 0xF0, 0x10, 0xF0, // 5
		0xF0, 0x80, 0xF0, 0x90, 0xF0, // 6
		0xF0, 0x10, 0x20, 0x40, 0x40, // 7
		0xF0, 0x90, 0xF0, 0x90, 0xF0, // 8
		0xF0, 0x90, 0xF0, 0x10, 0xF0, // 9
		0xF0, 0x90, 0xF0, 0x90, 0x90, // A
		0xE0, 0x90, 0xE0, 0x90, 0xE0, // B
		0xF0, 0x80, 0x80, 0x80, 0xF0, // C
		0xE0, 0x90, 0x90, 0x90, 0xE0, // D
		0xF0, 0x80, 0xF0, 0x80, 0xF0, // E
		0xF0, 0x80, 0xF0, 0x80, 0x80, // F
	}

	// load font
	for i = 0; i < uint16(len(font)); i++ {
		memory[fontOffset+i] = font[i]
	}

	pc = 0x200
}

func printState() {
	for i := 0; i < 16; i += 4 {
		fmt.Printf("V%02d: %02x  V%02d: %02x  V%02d: %02x  V%02d: %02x\n", i, registers[i], i+1, registers[i+1], i+2, registers[i+2], i+3, registers[i+3])
	}
}

// sine wave pulled from https://github.com/veandco/go-sdl2-examples/blob/master/examples/audio/audio.go
const (
	toneHz   = 440
	sampleHz = 48000
	dPhase   = 2 * math.Pi * toneHz / sampleHz
)

//export SineWave
func SineWave(userdata unsafe.Pointer, stream *C.Uint8, length C.int) {
	n := int(length)
	hdr := reflect.SliceHeader{Data: uintptr(unsafe.Pointer(stream)), Len: n, Cap: n}
	buf := *(*[]C.Uint8)(unsafe.Pointer(&hdr))

	var phase float64
	for i := 0; i < n; i += 2 {
		phase += dPhase
		sample := C.Uint8((math.Sin(phase) + 0.999999) * 128)
		buf[i] = sample
		buf[i+1] = sample
	}
}

func setupSDL() {
	var err error
	err = sdl.Init(sdl.INIT_AUDIO | sdl.INIT_VIDEO)
	check(err)

	window, err = sdl.CreateWindow("testies", sdl.WINDOWPOS_UNDEFINED, sdl.WINDOWPOS_UNDEFINED, 800, 600, sdl.WINDOW_SHOWN)
	check(err)

	surface, err = window.GetSurface()
	check(err)

	// audio

	audioSpec := sdl.AudioSpec{
		Freq:     sampleHz,
		Format:   sdl.AUDIO_U8,
		Channels: 2,
		Samples:  sampleHz,
		Callback: sdl.AudioCallback(C.SineWave),
	}

	err = sdl.OpenAudio(&audioSpec, nil)
	check(err)
}

func updateTimers() {

	ticker := time.NewTicker(time.Second / 60)

	for {
		select {
		case <-ticker.C:
			if delayTimer != 0 {
				delayTimer--
			}
			if soundTimer != 0 {
				soundTimer--
			}
		}
	}
}

func processInstructions() {
	if pc >= uint16(len(memory)) {
		return
	}
	for i := 0; i < loopSpeed; i++ {
		byte1 := memory[pc]
		byte2 := memory[pc+1]

		pc += 2

		instruction := byte1 >> 4 // IXYN
		X := byte1 & 0x0F
		Y := byte2 >> 4
		N := byte2 & 0x0F
		NN := byte2                      // --NN
		NNN := uint16(X)<<8 | uint16(NN) // -NNN

		if debug {
			fmt.Printf("%02x%02x\n", byte1, byte2)
			//	fmt.Printf("Instruction: %v -- %08b%08b\n", hex.EncodeToString([]byte{byte1, byte2}), byte1, byte2)
			//	fmt.Printf("I: %x\n", instruction)
			//	fmt.Printf("X: %x\n", X)
			//	fmt.Printf("Y: %x\n", Y)
			//	fmt.Printf("N: %x\n", N)
			//	fmt.Printf("NN: %02x\n", NN)
			//	fmt.Printf("NNN: %03x\n", NNN)
		}

		switch instruction {
		case 0x0:
			{
				if NNN == 0x0E0 {
					if debug {
						fmt.Println("00E0: clear screen")
					}
					for j := 0; j < 64; j++ {
						for k := 0; k < 32; k++ {
							display[j][k] = false
						}
					}
				} else if NNN == 0x0EE {
					if debug {
						fmt.Println("00EE: return from subroutine")
					}
					pc = pop()
				}
			}
		case 0x1:
			{
				if debug {
					fmt.Println("1NNN: jump")
				}
				pc = NNN
			}
		case 0x2:
			{
				if debug {
					fmt.Println("2NNN: call subroutine")
				}
				push(pc)
				pc = NNN
			}
		case 0x3:
			{
				if debug {
					fmt.Println("3XNN: skip, if VX == NN")
				}
				if registers[X] == NN {
					pc += 2
				}
			}
		case 0x4:
			{
				if debug {
					fmt.Println("4XNN: skip, if VX != NN")
				}
				if registers[X] != NN {
					pc += 2
				}
			}
		case 0x5:
			{
				if debug {
					fmt.Println("5XNN: skip, if VX == VY")
				}
				if registers[X] == registers[Y] {
					pc += 2
				}
			}
		case 0x6:
			{
				if debug {
					fmt.Println("6XNN: set register VX")
				}
				registers[X] = NN
			}
		case 0x7:
			{
				if debug {
					fmt.Println("7XNN: add value to register VX")
				}
				registers[X] = registers[X] + NN
			}
		case 0x8:
			{
				switch N {
				case 0x0:
					{
						if debug {
							fmt.Println("8XY0: set, VX = VY")
						}
						registers[X] = registers[Y]
					}
				case 0x1:
					{
						if debug {
							fmt.Println("8XY1: binary OR")
						}
						registers[X] = registers[X] | registers[Y]
					}
				case 0x2:
					{
						if debug {
							fmt.Println("8XY2: binary AND")
						}
						registers[X] = registers[X] & registers[Y]
					}
				case 0x3:
					{
						if debug {
							fmt.Println("8XY3: logical XOR")
						}
						registers[X] = registers[X] ^ registers[Y]
					}
				case 0x4:
					{
						if debug {
							fmt.Println("8XY4: add")
						}
						result := uint16(registers[X]) + uint16(registers[Y])

						if result&0x0100 == 0 {
							registers[0xF] = 0x00
						} else {
							registers[0xF] = 0x01
						}

						registers[X] = byte(result)
					}
				case 0x5:
					{
						if debug {
							fmt.Println("8XY5: subtract, VX - VY")
						}
						result := uint16(registers[X]) - uint16(registers[Y])

						if registers[X] > registers[Y] {
							registers[0xF] = 1
						} else {
							registers[0xF] = 0
						}

						registers[X] = byte(result)
					}
				case 0x6:
					{
						if debug {
							fmt.Println("8XY6: shift right")
						}
						if cosmacShift {
							registers[X] = registers[Y]
						}

						bit := registers[X] & 0x01

						registers[X] = registers[X] >> 1
						registers[0xF] = bit
					}
				case 0x7:
					{
						if debug {
							fmt.Println("8XY7: Subtract, VY - VX")
						}
						result := uint16(registers[Y]) - uint16(registers[X])

						if registers[Y] > registers[X] {
							registers[0xF] = 1
						} else {
							registers[0xF] = 0
						}

						registers[X] = byte(result)
					}
				case 0xE:
					{
						if debug {
							fmt.Println("8XYE: shift left")
						}
						if cosmacShift {
							registers[X] = registers[Y]
						}

						var bit byte
						if registers[X]&0x80 == 0 {
							bit = 0
						} else {
							bit = 1
						}

						registers[X] = registers[X] << 1
						registers[0xF] = bit

						//registers[0xF] = registers[X] & 0b10000000
						//registers[X] = registers[X] << 1
					}
				}
			}
		case 0x9:
			{
				if debug {
					fmt.Println("9XY0: skip, if VX != VY")
				}
				if registers[X] != registers[Y] {
					pc += 2
				}
			}
		case 0xA:
			{
				if debug {
					fmt.Println("ANNN: set index register I")
				}
				indexRegister = NNN
			}
		case 0xB:
			{
				if debug {
					fmt.Println("BNNN/BXNN: jump with offset")
				}
				if superchipJumpWithOffset {
					pc = NNN + uint16(registers[X])
				} else {
					pc = NNN + uint16(registers[0])
				}
			}
		case 0xC:
			{
				if debug {
					fmt.Println("CXNN: random")
				}

				registers[X] = byte(rand.Int()) & NN
			}
		case 0xD:
			{
				if debug {
					fmt.Println("DXYN: display/draw")
				}
				x := int(registers[X] % 64)
				y := int(registers[Y] % 32)
				height := uint16(N)

				registers[0xF] = 0

				for j := indexRegister; j < (indexRegister+height) && y < 32; j, y = j+1, y+1 {

					data := memory[j]

					var on [8]bool
					on[0] = data&0b10000000 != 0
					on[1] = data&0b01000000 != 0
					on[2] = data&0b00100000 != 0
					on[3] = data&0b00010000 != 0
					on[4] = data&0b00001000 != 0
					on[5] = data&0b00000100 != 0
					on[6] = data&0b00000010 != 0
					on[7] = data&0b00000001 != 0

					for xOffset := 0; xOffset < 8 && x+xOffset < 64; xOffset++ {
						if on[xOffset] && display[x+xOffset][y] {
							display[x+xOffset][y] = false
							registers[0xF] = 1
						} else if on[xOffset] {
							display[x+xOffset][y] = true
						}
					}
				}
			}
		case 0xE:
			{
				if debug {
					fmt.Println("EX9E/EXA1: skip if (not) pressed")
				}
				if Y == 0x9 {
					if checkKeyIsPressed(registers[X]) {
						pc += 2
					}
				} else if Y == 0xA {
					if !checkKeyIsPressed(registers[X]) {
						pc += 2
					}
				}
			}
		case 0xF:
			{
				switch NN {
				case 0x07:
					{
						if debug {
							fmt.Println("FX07: set VX to delay timer value")
						}
						registers[X] = delayTimer
					}
				case 0x15:
					{
						if debug {
							fmt.Println("FX15: set delay timer to VX value")
						}
						delayTimer = registers[X]
					}
				case 0x18:
					{
						if debug {
							fmt.Println("FX18: set sound timer to VX value")
						}
						soundTimer = registers[X]
					}
				case 0x1E:
					{
						if debug {
							fmt.Println("FX1E: add to index")
						}
						indexRegister = indexRegister & 0x0FFF // only keep 12 addressable bits
						indexRegister += uint16(registers[X])

						if indexRegister&0x1000 == 1 { // use 13th bit for overflow
							registers[0xF] = 1
						}
					}
				case 0x0A:
					{
						if debug {
							fmt.Println("FX0A: get key, blocking")
						}
						key, ok := getKeyPressed()
						if !ok {
							pc -= 2 // blocks until key is pressed
						} else {
							registers[X] = key
						}
					}
				case 0x29:
					{
						if debug {
							fmt.Println("FX29: font character")
						}
						indexRegister = fontOffset + (uint16(registers[X]) * 5)
					}
				case 0x33:
					{
						if debug {
							fmt.Println("FX33: binary-coded decimal conversion")
						}
						RX := uint16(registers[X])
						memory[indexRegister+2] = byte(RX % 10)
						RX = RX / 10
						memory[indexRegister+1] = byte(RX % 10)
						memory[indexRegister] = byte(RX / 10)
					}
				case 0x55:
					{
						if debug {
							fmt.Println("FX55: store memory")
						}
						for v := 0x0; v <= int(X); v++ {
							memory[indexRegister+uint16(v)] = registers[v]
						}
						//	indexRegister += uint16(X) + 1    			// OG CHIP Behavior
					}
				case 0x65:
					{
						if debug {
							fmt.Println("FX65: load memory")
						}
						for v := 0x0; v <= int(X); v++ {
							registers[v] = memory[indexRegister+uint16(v)]
						}
						//	indexRegister += uint16(X) + 1    			// OG CHIP Behavior
					}
				}
			}
		}

		if debug {
			//	fmt.Println("----")
			printState()
		}
	}
}

func checkKeyIsPressed(x byte) bool {
	keyboard = sdl.GetKeyboardState()
	return keyboard[byteToScancode[x]] != 0
}

type KeypadState uint8

const (
	NOT_PRESSED KeypadState = 0
	PRESSED     KeypadState = 1
)

func resetKeypadState() {
	for i := range keypadStates {
		keypadStates[i] = NOT_PRESSED
	}
}

func getKeyPressed() (byte, bool) {
	if !keypadStateInit {
		resetKeypadState()
		keypadStateInit = true
	}

	keyboard = sdl.GetKeyboardState()

	// random map iteration currently resolves which key is determined "pressed"
	for k, v := range scancodeToByte {
		if keyboard[k] != 0 {
			keypadStates[v] = PRESSED
		} else if keyboard[k] == 0 && keypadStates[v] == PRESSED {
			keypadStateInit = false
			return v, true
		}
	}

	return 0, false
}

func pumpEvents() {
	keyboard = sdl.GetKeyboardState()

	for event := sdl.PollEvent(); event != nil; event = sdl.PollEvent() {
		switch event.(type) {
		case *sdl.QuitEvent:
			fmt.Println("Quit")
			running = false
			break
		}
	}

	if keyboard[sdl.SCANCODE_ESCAPE] != 0 {
		fmt.Println("Quit")
		running = false
	}
	if keyboard[sdl.SCANCODE_BACKSPACE] != 0 {
		fmt.Println("stepThrough")
		stepThrough = !stepThrough
	}
	if keyboard[sdl.SCANCODE_UP] != 0 {
		loopSpeed++
		fmt.Printf("loopspeed: %v", loopSpeed)
	}
	if keyboard[sdl.SCANCODE_DOWN] != 0 {
		if loopSpeed > 1 {
			loopSpeed--
		}
		fmt.Printf("loopspeed: %v", loopSpeed)
	}
}

func render() {
	err := surface.FillRect(nil, 0)
	check(err)

	var scale int32 = 10
	var x, y int32
	for x = 0; x < 64; x++ {
		for y = 0; y < 32; y++ {
			if display[x][y] {
				err = surface.FillRect(&sdl.Rect{X: x * scale, Y: y * scale, W: scale, H: scale}, 0xffffffff)
				check(err)
			}
		}
	}

	err = window.UpdateSurface()
	check(err)
}

func shutdown() {
	err := window.Destroy()
	check(err)
	sdl.Quit()
}

var play bool

func playAudio() {
	if play && soundTimer == 0 {
		sdl.PauseAudio(true)
		play = false
	} else if !play && soundTimer != 0 {
		sdl.PauseAudio(false)
		play = true
	}
}

func main() {
	defer shutdown()

	reader := bufio.NewReader(os.Stdin)

	go updateTimers()

	for running {
		pumpEvents()

		processInstructions()

		playAudio()
		render()

		if stepThrough {
			reader.ReadString('\n')
		}
	}
}
