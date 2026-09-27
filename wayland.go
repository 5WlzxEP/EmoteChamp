package main

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"io"
	"io/fs"
	"log"
	"log/slog"
	"math"
	"math/rand"
	"os"
	"path"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"

	draw2 "golang.org/x/image/draw"
	"golang.org/x/sys/unix"
)

type state struct {
	fd        int
	currentId uint32

	wlRegistry   uint32
	wlSHM        uint32
	wlSHMPool    uint32
	wlBuffer     uint32
	xdgWMBase    uint32
	xdgSurface   uint32
	wlCompositor uint32
	wlSurface    uint32
	xdgToplevel  uint32
	viewport     uint32
	wpViewporter uint32
	// Server-side decorations (zxdg_decoration_manager_v1)
	zxdgDecoManager        uint32
	zxdgToplevelDecoration uint32
	decoration             atomic.Bool
	// Pointer / input (wl_seat, wl_pointer)
	wlSeat             uint32
	wlPointer          uint32
	ptrX               int32
	ptrY               int32
	pointerOverSurface bool
	stride             uint32
	w                  uint32
	h                  uint32
	viewWidth          uint32
	viewHeight         uint32
	// Dimensions of the currently created wl_buffer (for resize detection).
	bufferW     uint32
	bufferH     uint32
	shmPoolSize uint32
	shmFD       int
	lock        sync.Mutex
	shmPoolData []byte

	// icon
	iconManager uint32
	iconPool    uint32
	iconInstant uint32
	IconSizes   []uint32
	IconBuffer  []byte
	IconFd      int
	IconFdSize  int
	iconMap     []byte

	// shape
	cursorShapeManager uint32
	cursorShapeDevice  uint32
	cursorCurrent      Cursor
	cursorLatestEnter  uint32

	// drag and drop
	dataDeviceManager uint32
	dataDevice        uint32
	dataOffer         uint32
	dataTypes         []string

	plasmaShell   uint32
	plasmaSurface uint32

	canAttach atomic.Bool

	redraw atomic.Bool

	srcWidth  uint32
	srcHeight uint32
	clicked   bool
}

// waylandDisplayConnect connects to the Wayland compositor via a Unix socket.
func waylandDisplayConnect() (state, error) {
	xdgRuntimeDir := os.Getenv("XDG_RUNTIME_DIR")
	if xdgRuntimeDir == "" {
		return state{}, fmt.Errorf("XDG_RUNTIME_DIR not set")
	}
	waylandDisplay := os.Getenv("WAYLAND_DISPLAY")
	if waylandDisplay == "" {
		waylandDisplay = "wayland-0"
	}
	socketPath := xdgRuntimeDir + "/" + waylandDisplay

	addr := unix.SockaddrUnix{Name: socketPath}
	fd, err := unix.Socket(unix.AF_UNIX, unix.SOCK_STREAM, 0)
	if err != nil {
		return state{}, fmt.Errorf("socket: %w", err)
	}
	if err := unix.Connect(fd, &addr); err != nil {
		_ = unix.Close(fd)
		return state{}, fmt.Errorf("connect: %w", err)
	}

	return state{fd: fd, currentId: 1}, nil
}

func (s *state) waylandWLDisplayGetRegistry() {
	var msg [12]byte

	binary.LittleEndian.PutUint32(msg[0:], waylandDisplayObjectID)
	binary.LittleEndian.PutUint16(msg[4:], waylandWLDisplayGetRegistryOpcode)

	msgAnnouncedSize := waylandHeaderSize + 4
	binary.LittleEndian.PutUint16(msg[6:], msgAnnouncedSize)

	id := atomic.AddUint32(&s.currentId, 1)
	binary.LittleEndian.PutUint32(msg[8:], id)

	n, err := unix.Write(s.fd, msg[:])
	if err != nil || n != 12 {
		_, _ = fmt.Fprintf(os.Stderr, "send error: %v\n", err)
		os.Exit(1)
	}
	//log.Printf("-> wl_display@%d.get_registry: wl_registry=%d\n", waylandDisplayObjectID, s.currentId)
	slog.Debug("->wl_display@get_registry", "wlDisplay", waylandDisplayObjectID, "wlRegistry", id)
	s.wlRegistry = id
}

func (s *state) Close() error {
	var err error
	if s.fd != 0 {
		err = unix.Close(s.fd)
		s.fd = 0
	}

	if s.wlBuffer != 0 {
		s.wlBufferDestroy()
	}

	s.lock.Lock()
	if s.shmPoolData != nil {
		e := unix.Munmap(s.shmPoolData)
		s.shmPoolData = nil
		err = errors.Join(err, e)
	}
	s.lock.Unlock()

	if s.shmFD != 0 {
		e := unix.Close(s.shmFD)
		s.shmFD = 0
		err = errors.Join(err, e)
	}

	if s.IconFd != 0 {
		e := unix.Close(s.IconFd)
		s.IconFd = 0
		err = errors.Join(err, e)
	}

	if s.iconMap != nil {
		e := unix.Munmap(s.iconMap)
		s.iconMap = nil
		err = errors.Join(err, e)
	}

	return err
}

func (s *state) readMessages(cancel func()) {
	var readBuf [4096]byte
	var pending int
	for {
		n, err := unix.Read(s.fd, readBuf[pending:])
		n += pending
		pending = 0
		if n == 0 && err == nil {
			// Compositor closed the connection.
			//os.Exit(0)
			cancel()
			break
		}
		if err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "read error: %v\n", err)
			os.Exit(1)
		}

		for i := 0; n-pending >= 8; i++ {
			announced := int(binary.LittleEndian.Uint16(
				readBuf[pending+6 : pending+8],
			))

			if announced < 8 {
				panic(fmt.Sprintf("invalid message length: %d", announced))
			}
			if announced > len(readBuf) {
				panic(
					fmt.Sprintf(
						"message length %d exceeds buffer size %d",
						announced,
						len(readBuf),
					))
			}
			if announced%4 != 0 {
				panic("message length not multiple of 4")
			}

			if n-pending < announced {
				break
			}

			s.handleEvent(readBuf[pending : pending+announced])
			pending += announced
		}

		copy(readBuf[:], readBuf[pending:n])
		pending = n - pending
	}
}

func (s *state) handleEvent(buf []byte) {
	start := 0
	off := new(0)
	objectID := bufReadU32(buf, off)
	opcode := bufReadU16(buf, off)
	announcedSize := bufReadU16(buf, off)

	// Ensure the full announced message is present in the buffer.
	// annoucedSize includes the 8-byte header.
	bufNeed(buf, start, int(announcedSize))

	switch objectID {
	case 0:
		fmt.Println("Invalid object ID")
	case s.wlRegistry:
		s.handleRegistryEvent(opcode, buf[*off:])
	case waylandDisplayObjectID:
		s.handleDisplayEvent(opcode, buf[*off:])
	case s.wlSHM:
		s.handleSHMEvent(opcode, buf[*off:])
	case s.wlBuffer:
		s.handleBufferEvent(opcode, buf[*off:])
	case s.xdgWMBase:
		s.handleXDGWMBaseEvent(opcode, buf[*off:])
	case s.xdgToplevel:
		s.handleXDGTopLevelEvent(opcode, buf[*off:])
	case s.zxdgToplevelDecoration:
		s.handleZxdgToplevelDecorationEvent(opcode, buf[*off:])
	case s.xdgSurface:
		s.handleXdgSurfaceEvent(opcode, buf[*off:])
	case s.wlSurface:
		s.handleWlSurfaceEvent(opcode, buf[*off:])
	case s.wlPointer:
		s.handleWlPointerEvent(opcode, buf[*off:])
	case s.wlSeat:
		s.handleWlSeatEvent(opcode, buf[*off:])
	case s.iconManager:
		s.handleIconManagerEvent(opcode, buf[*off:])
	case s.dataDevice:
		s.handleDataDeviceEvent(opcode, buf[*off:])
	case s.dataOffer:
		s.handleDataOfferEvent(opcode, buf[*off:])
	default:
		_, _ = fmt.Fprintf(os.Stderr, "unhandled: object_id=%d opcode=%d\n", objectID, opcode)
	}
}

func (s *state) handleRegistryEvent(opcode uint16, msg []byte) {
	var offset = 0
	switch opcode {
	case waylandWLRegistryEventGlobal:
		name := bufReadU32(msg, &offset)
		ifaceLen := bufReadU32(msg, &offset)
		iface := bufReadN(msg, &offset, int(roundup4(ifaceLen)))
		version := bufReadU32(msg, &offset)

		switch ifaceName := strings.TrimRight(string(iface[:ifaceLen]), "\x00"); ifaceName {
		case "wl_shm":
			s.wlSHM = s.waylandWLRegistryBind(name, iface, ifaceLen, version)
		case "xdg_wm_base":
			s.xdgWMBase = s.waylandWLRegistryBind(name, iface, ifaceLen, version)
		case "wl_compositor":
			s.wlCompositor = s.waylandWLRegistryBind(name, iface, ifaceLen, version)
		case "zxdg_decoration_manager_v1":
			// v2 (as advertised) relaxes get_toplevel_decoration to allow
			// use after a buffer is attached/committed, so take it.
			bindVersion := min(version, 2)
			s.zxdgDecoManager = s.waylandWLRegistryBind(name, iface, ifaceLen, bindVersion)
		case "wl_seat":
			bindVersion := min(version, 7)
			s.wlSeat = s.waylandWLRegistryBind(name, iface, ifaceLen, bindVersion)
		case "wp_viewporter":
			s.wpViewporter = s.waylandWLRegistryBind(name, iface, ifaceLen, version)
		case "xdg_toplevel_icon_manager_v1":
			s.iconManager = s.waylandWLRegistryBind(name, iface, ifaceLen, version)
		case "org_kde_plasma_shell":
			s.plasmaShell = s.waylandWLRegistryBind(name, iface, ifaceLen, version)
		case "wp_cursor_shape_manager_v1":
			s.cursorShapeManager = s.waylandWLRegistryBind(name, iface, ifaceLen, version)
		case "wl_data_device_manager":
			s.dataDeviceManager = s.waylandWLRegistryBind(name, iface, ifaceLen, version)
		default:
			slog.Debug("Not binding to", "iface", ifaceName, "version", version)
		}

		//case waylandWLRegistryEventGlobalRemove:
		// currently ignored, since name isn't saved for each registry
	}
}

func (s *state) handleDisplayEvent(opcode uint16, msg []byte) {
	off := 0
	switch opcode {
	case waylandWLDisplayErrorEvent:
		targetID := bufReadU32(msg, &off)
		code := bufReadU32(msg, &off)
		errorLen := bufReadU32(msg, &off)
		errorMsg := bufReadN(msg, &off, int(roundup4(errorLen)))
		slog.Error("fatal error", "targetID", targetID, "code", code, "error", bufReadString(errorMsg))
	case waylandWLDisplayEventDeleteID:
		delID := bufReadU32(msg, &off)
		slog.Debug("-> wl_display@delete_id", "delID", delID)
	}
}

func (s *state) handleSHMEvent(opcode uint16, msg []byte) {
	off := 0
	switch opcode {
	case waylandSHMPoolEventFormat:
		format := bufReadU32(msg, &off)
		slog.Debug("-> wl_shm_pool@format", "wl_shm", s.wlSHM, "format", format, "formatName", colorFormatToName(format))
	}
}

func (s *state) handleBufferEvent(opcode uint16, _ []byte) {
	switch opcode {
	case waylandWLBufferEventRelease:
		slog.Debug("-> wl_buffer@release", "wlBuffer", s.wlBuffer)
		s.wlBuffer = 0
	}
}

func (s *state) handleXDGWMBaseEvent(opcode uint16, msg []byte) {
	var off int
	switch opcode {
	case waylandXdgWMBaseEventPing:
		ping := bufReadU32(msg, &off)
		slog.Debug("-> xdg_wm_base@ping", "xdgWMBase", s.xdgWMBase, "ping", ping)
		s.waylandXDGWMBasePong(ping)
	}
}

func (s *state) handleXDGTopLevelEvent(opcode uint16, msg []byte) {
	var off int
	switch opcode {
	case waylandXdgToplevelEventConfigure:
		width := bufReadU32(msg, &off)
		height := bufReadU32(msg, &off)
		statesLen := bufReadU32(msg, &off)
		states := bufReadN(msg, &off, int(statesLen))
		var state []string
		for i := 0; i < int(statesLen); i += 4 {
			state = append(state, xdgTopLevelStateToName(binary.NativeEndian.Uint32(states[i:i+4])))
		}

		//log.Printf("<- xdg_toplevel@%d.configure: width=%d height=%d, states=%v\n", s.xdgToplevel, width, height, state)
		slog.Debug("-> xdg_toplevel@configure", "xdgToplevel", s.xdgToplevel, "width", width, "height", height, "states", state)
		if width > 0 && height > 0 && (width != s.w || height != s.h) {
			//log.Printf("   resize: %dx%d -> %dx%d\n", s.w, s.h, width, height)
			//s.lock.Lock()
			dx := float64(width) / float64(s.srcWidth)
			dy := float64(height) / float64(s.srcHeight)
			d := min(dx, dy)
			s.viewWidth = uint32(math.Round(float64(s.srcWidth) * d))
			s.viewHeight = uint32(math.Round(float64(s.srcHeight) * d))
			//s.stride = s.w * colorChannels
			//s.lock.Unlock()

			s.viewportSetDestination(
				uint32(math.Round(float64(s.srcWidth)*d)),
				uint32(math.Round(float64(s.srcHeight)*d)),
			)
			//s.waylandWLSurfaceCommit()
			s.redraw.Store(true)
		}

	case waylandXdgToplevelEventClose:
		slog.Log(context.Background(), -5, "-> xdg_toplevel@close", "surface", s.xdgToplevel)
		s.xdgToplevel = 0
	case waylandXdgToplevelEventConfigureBounds:
		width := bufReadU32(msg, &off)
		height := bufReadU32(msg, &off)
		slog.Log(context.Background(), -5, "-> xdg_toplevel@configure_bounds", "surface", s.xdgToplevel, "width", width, "height", height)
	case waylandXdgToplevelEventWMCapabilities:
		capLen := bufReadU32(msg, &off)
		res := bufReadN(msg, &off, int(roundup4(capLen)))
		var capabilities []string
		for i := 0; i < int(capLen); i += 4 {
			capabilities = append(capabilities, xdgTopLevelCapabilitiesToName(binary.NativeEndian.Uint32(res[i:i+4])))
		}
		slog.Debug("-> xdg_toplevel@wm_capabilities", "wm", s.xdgWMBase, "capabilities", capabilities)
	}
}

func (s *state) handleZxdgToplevelDecorationEvent(opcode uint16, msg []byte) {
	var off int

	switch opcode {
	case waylandZxdgToplevelDecorationEventConfigure:
		modeInt := bufReadU32(msg, &off)
		var mode string
		switch modeInt {
		case 1:
			mode = "client_side"
		case 2:
			mode = "server_side"
		}
		//log.Printf("<- zxdg_toplevel_decoration@%d.configure: mode=%s\n", s.zxdgToplevelDecoration, mode)
		slog.Debug("-> zxdg_toplevel_decoration@configure", "zxdg_toplevel_decoration", s.zxdgToplevelDecoration, "mode", mode)
	}
}

func (s *state) handleXdgSurfaceEvent(opcode uint16, msg []byte) {
	if opcode == waylandXdgSurfaceEventConfigure {
		configure := binary.LittleEndian.Uint32(msg)
		//log.Printf("<- xdg_surface@%d.configure: %d\n", s.xdgSurface, configure)
		slog.Debug("-> xdg_surface@configure", "xdgSurface", s.xdgSurface, "configure", configure)
		s.waylandXDGSurfaceAckConfigure(configure)
		if s.redraw.CompareAndSwap(true, false) {
			//s.viewportSetDestination(s.w, s.h)
			//s.waylandWLSurfaceCommit()
			s.drawContent()
		}
	}
}

func (s *state) handleWlSurfaceEvent(opcode uint16, msg []byte) {
	var off int
	switch opcode {
	case waylandWLSurfaceEventEnter:
		output := bufReadU32(msg, &off)
		//log.Printf("<- wl_surface@%d.enter: output=%d\n", s.wlSurface, output)
		slog.Debug("-> wl_surface@enter", "wlSurface", s.wlSurface, "output", output)
	case waylandWLSurfaceEventLeave:
		output := bufReadU32(msg, &off)
		//log.Printf("<- wl_surface@%d.leave: output=%d\n", s.wlSurface, output)
		slog.Debug("-> wl_surface@leave", "wlSurface", s.wlSurface, "output", output)
	case waylandWLSurfaceEventPreferredBufferScale:
		factor := bufReadU32(msg, &off)
		//log.Printf("<- wl_surface@%d.preferred_buffer_scale: %d (HiDPI not implemented; rendering unscaled)\n",
		//	s.wlSurface, factor)
		slog.Debug("-> wl_surface@preferred_buffer_scale (HiDPI not implemented; rendering unscaled)", "wlSurface", s.wlSurface, "factor", factor)
	case waylandWLSurfaceEventPreferredBufferTransform:
		transform := bufReadU32(msg, &off)
		//log.Printf("<- wl_surface@%d.preferred_buffer_transform: %d (not implemented)\n", s.wlSurface, transform)
		slog.Debug("-> wl_surface@preferred_buffer_transform (not implemented)", "surface", s.wlSurface, "transform", transform)
	}
}

func (s *state) handleWlPointerEvent(opcode uint16, msg []byte) {
	const (
		Enter = iota
		Leave
		Motion
		Button
		Axis
		Frame
		AxisSource
		AxisStop
		AxisDiscrete
		AxisValue120 // Deprecated
		AxisRelativeDirection
		Wrap
	)

	offset := 0
	switch opcode {
	case Enter:
		serial := bufReadU32(msg, &offset)
		bufReadU32(msg, &offset) // surface
		sx := int32(bufReadU32(msg, &offset)) >> 8
		sy := int32(bufReadU32(msg, &offset)) >> 8
		s.ptrX, s.ptrY = sx, sy
		s.pointerOverSurface = true
		s.cursorLatestEnter = serial
		//log.Printf("<- wl_pointer@%d.enter: serial=%d pos=%d,%d\n", s.wlPointer, serial, sx, sy)
		slog.Debug("-> wl_pointer@enter", "wlPointer", s.wlPointer, "serial", serial, "sx", sx, "sy", sy)
		updateCursor(s)
	case Leave:
		serial := bufReadU32(msg, &offset)
		bufReadU32(msg, &offset) // surface
		s.pointerOverSurface = false
		//log.Printf("<- wl_pointer@%d.leave: serial=%d\n", s.wlPointer, serial)
		slog.Debug("-> wl_pointer@leave", "wlPointer", s.wlPointer, "serial", serial)
		s.cursorCurrent = 0
	case Motion:
		bufReadU32(msg, &offset)
		sx := int32(bufReadU32(msg, &offset)) >> 8
		sy := int32(bufReadU32(msg, &offset)) >> 8
		s.ptrX, s.ptrY = sx, sy

	case Button:
		serial := bufReadU32(msg, &offset)
		bufReadU32(msg, &offset) // time
		button := bufReadU32(msg, &offset)
		state := bufReadU32(msg, &offset)
		//log.Printf("<- wl_pointer@%d.button: serial=%d button=%#x state=%d\n", s.wlPointer, serial, button, state)
		slog.Debug("-> wl_pointer@button \n", "wlPointer", s.wlPointer, "serial", serial, "button", button, "state", state)

		if button == waylandPointerButtonLeft && state == 1 {
			// Left button pressed inside the bottom-right grab area:
			// hand control to the compositor for an interactive resize.
			inCorner := s.pointerOverSurface &&
				s.ptrX >= int32(s.viewWidth)-max(resizeHandleSize, int32(0.1*float32(s.viewWidth))) &&
				s.ptrY >= int32(s.viewHeight)-max(resizeHandleSize, int32(0.1*float32(s.viewHeight)))
			if inCorner {
				s.waylandXDGToplevelResize(serial, waylandXdgResizeEdgeBottomRight)
				s.redraw.Store(true)
			} else {
				//slog.Info("clicked", "state", !s.clicked)
				s.clicked = !s.clicked
				s.drawContent()
			}
		}
	case Axis:
	// scroll events, maybe zoom?
	case Frame:
		//ignore
		updateCursor(s)
	default:
		log.Printf("-> wl_pointer@%d.%d: unhandled\n", s.wlPointer, opcode)
	}
}

func updateCursor(s *state) {
	inCorner := s.pointerOverSurface &&
		s.ptrX >= int32(s.viewWidth)-max(resizeHandleSize, int32(0.1*float32(s.viewWidth))) &&
		s.ptrY >= int32(s.viewHeight)-max(resizeHandleSize, int32(0.1*float32(s.viewHeight)))
	if inCorner && s.cursorCurrent != nwseResizecursor {
		s.ShapeSetCursor(s.cursorLatestEnter, nwseResizecursor)
		s.cursorCurrent = nwseResizecursor
	} else if !inCorner && s.cursorCurrent != defaultCursor {
		s.ShapeSetCursor(s.cursorLatestEnter, defaultCursor)
		s.cursorCurrent = defaultCursor
	}
}

func (s *state) handleWlSeatEvent(opcode uint16, _ []byte) {
	const (
		Capabilities = iota
		Name
	)
	//var off int
	switch opcode {
	case Capabilities:
		//capabilities := bufReadU32(buf, off)
		//log.Printf("-> wl_seat@%d.capabilities: %d\n", objectID, capabilities)
		// 1 pointer, 2 keyboard, 4 touch; bitmask
	case Name:
		//name := bufReadN(buf, off, int(announcedSize)-8)
		//for name[len(name)-1] == 0 {
		//	name = name[:len(name)-1]
		//}
		//log.Printf("-> wl_seat@%d.name: %s\n", objectID, name)
	}
}

func (s *state) handleIconManagerEvent(opcode uint16, buf []byte) {
	const (
		IconSize = iota
		Done
	)

	switch opcode {
	case IconSize:
		size := binary.LittleEndian.Uint32(buf)
		slog.Debug("-> icon_manager@icon_size", "iconManager", s.iconManager, "size", size)
		s.IconSizes = append(s.IconSizes, size)
	case Done:
		slog.Debug("-> icon_manager@done", "iconManager", s.iconManager)
		s.setupIcon()
	}
}

func (s *state) handleDataDeviceEvent(opcode uint16, buf []byte) {
	const (
		DataOffer = iota
		Enter
		Leave
		Motion
		Drop
		Selection
	)

	switch opcode {
	case DataOffer:
		s.dataOffer = binary.LittleEndian.Uint32(buf)
		slog.Debug("-> data_device@data_offer", "dataDevice", s.dataDevice, "dataOffer", s.dataOffer)
	case Enter:
		if len(buf) != 20 {
			slog.Error("invalid data device enter event", "dataDevice", s.dataDevice, "opcode", opcode, "buf", buf)
		}
		serial := binary.LittleEndian.Uint32(buf)
		surface := binary.LittleEndian.Uint32(buf[4:])
		x := binary.LittleEndian.Uint32(buf[8:])
		y := binary.LittleEndian.Uint32(buf[12:])
		id := binary.LittleEndian.Uint32(buf[16:])
		slog.Debug("-> data_device@enter", "dataDevice", s.dataDevice, "serial", serial, "surface", surface, "x", x, "y", y, "id", id)

	case Leave:
		slog.Debug("-> data_device@leave", "dataDevice", s.dataDevice)
		s.dataOffer = 0
		s.dataTypes = nil
	case Motion:
	// ignore
	case Drop:
		slog.Debug("-> data_device@drop", "dataDevice", s.dataDevice)

	// TODO accept/deny
	case Selection:
		//s.dataOffer = binary.LittleEndian.Uint32(buf)
		slog.Debug("-> data_device@selection", "dataDevice", s.dataDevice, "dataOffer", s.dataOffer)
	}
}

func (s *state) handleDataOfferEvent(opcode uint16, buf []byte) {
	const (
		Offer = iota
		SourceActions
		Action
	)
	switch opcode {
	case Offer:
		mimeLen := binary.LittleEndian.Uint32(buf)
		mimeType := bufReadString(buf[4 : 4+mimeLen])
		slog.Debug("-> data_offer@offer", "dataDevice", s.dataDevice, "mimeLen", mimeLen, "mimeType", mimeType)

		//if mimeType == "TEXT" {
		//	s.dataOfferAccept(0, []byte(mimeType))
		//}
		s.dataTypes = append(s.dataTypes, mimeType)
	case SourceActions:
		actions := binary.LittleEndian.Uint32(buf)
		slog.Debug("-> data_device@source_actions", "dataDevice", s.dataDevice, "actions", actions)
	case Action:
		actions := binary.LittleEndian.Uint32(buf)
		slog.Debug("-> data_device@action", "dataDevice", s.dataDevice, "actions", actions)
	}
}

func (s *state) waylandWLRegistryBind(name uint32, iface []byte, ifaceLen, version uint32) uint32 {
	var msg [512]byte
	off := 0

	binary.LittleEndian.PutUint32(msg[off:], s.wlRegistry)
	off += 4
	binary.LittleEndian.PutUint16(msg[off:], waylandWLRegistryBindOpcode)
	off += 2

	msgAnnouncedSize := waylandHeaderSize + 4 + 4 + uint16(roundup4(ifaceLen)) + 4 + 4
	binary.LittleEndian.PutUint16(msg[off:], msgAnnouncedSize)
	off += 2

	binary.LittleEndian.PutUint32(msg[off:], name)
	off += 4
	binary.LittleEndian.PutUint32(msg[off:], ifaceLen)
	off += 4
	copy(msg[off:], iface)
	paddedLen := roundup4(ifaceLen)
	off += int(paddedLen)
	binary.LittleEndian.PutUint32(msg[off:], version)
	off += 4

	id := atomic.AddUint32(&s.currentId, 1)
	binary.LittleEndian.PutUint32(msg[off:], id)
	off += 4

	n, err := unix.Write(s.fd, msg[:off])
	if err != nil || n != off {
		_, _ = fmt.Fprintf(os.Stderr, "send error: %v\n", err)
		os.Exit(1)
	}
	//log.Printf("-> wl_registry@%d.bind: name=%d interface=%s version=%d id=%d\n", s.wlRegistry, name, bufReadString(iface), version, s.currentId)
	slog.Debug("<- wl_registry@bind", "registry", s.wlRegistry, "name", name, "interface", bufReadString(iface), "version", version, "id", id)
	return id
}

func (s *state) waylandWLSurfaceDamage() {
	if s.wlSurface == 0 {
		return
	}

	var msg [24]byte

	binary.LittleEndian.PutUint32(msg[:], s.wlSurface)
	binary.LittleEndian.PutUint16(msg[4:], waylandWLSurfaceDamageOpcode)
	binary.LittleEndian.PutUint16(msg[6:], waylandHeaderSize+4*4)
	binary.LittleEndian.PutUint32(msg[8:], 0)                       // x
	binary.LittleEndian.PutUint32(msg[12:], 0)                      // y
	binary.LittleEndian.PutUint32(msg[16:], max(s.w, s.viewWidth))  // width
	binary.LittleEndian.PutUint32(msg[20:], max(s.h, s.viewHeight)) // height

	_, _ = unix.Write(s.fd, msg[:])
	slog.Log(context.Background(), -5, "<- wl_surface@damage", "surface", s.wlSurface, "width", s.w, "height", s.h)
}

func (s *state) waylandWLSurfaceAttach() {
	var msg [20]byte

	binary.LittleEndian.PutUint32(msg[0:4], s.wlSurface)
	binary.LittleEndian.PutUint16(msg[4:6], waylandWLSurfaceAttachOpcode)
	binary.LittleEndian.PutUint16(msg[6:8], waylandHeaderSize+4+4+4)
	binary.LittleEndian.PutUint32(msg[8:12], s.wlBuffer)
	binary.LittleEndian.PutUint32(msg[12:16], 0) // x
	binary.LittleEndian.PutUint32(msg[16:20], 0) // y

	for !s.canAttach.Load() {
		runtime.Gosched()
	}

	_, _ = unix.Write(s.fd, msg[:])
	slog.Log(context.Background(), -5, "<- wl_surface@attach", "surface", s.wlSurface, "buffer", s.wlBuffer)
}

func (s *state) waylandWLSurfaceCommit() {
	s.drawBounds()

	var msg [8]byte

	binary.LittleEndian.PutUint32(msg[:4], s.wlSurface)
	binary.LittleEndian.PutUint16(msg[4:], waylandWLSurfaceCommitOpcode)
	binary.LittleEndian.PutUint16(msg[6:], waylandHeaderSize)

	_, _ = unix.Write(s.fd, msg[:])
	slog.Log(context.Background(), -5, "<- wl_surface@commit", "surface", s.wlSurface)
}

func (s *state) wlBufferDestroy() {
	var msg [8]byte

	binary.LittleEndian.PutUint32(msg[:4], s.wlBuffer)
	binary.LittleEndian.PutUint16(msg[4:], 0)
	binary.LittleEndian.PutUint16(msg[6:], waylandHeaderSize)

	_, _ = unix.Write(s.fd, msg[:])
	slog.Debug("<- wl_buffer@destroy", "buffer", s.wlBuffer)
}

func (s *state) waylandXDGWMBasePong(ping uint32) {
	var msg [12]byte
	binary.LittleEndian.PutUint32(msg[:4], s.xdgWMBase)
	binary.LittleEndian.PutUint16(msg[4:], waylandXdgWMBasePongOpcode)
	binary.LittleEndian.PutUint16(msg[6:], waylandHeaderSize+4)
	binary.LittleEndian.PutUint32(msg[8:], ping)

	_, _ = unix.Write(s.fd, msg[:])
	slog.Debug("<- xdg_wm_base@pong", "wm", s.xdgWMBase, "ping", ping)
}

func (s *state) waylandXDGSurfaceAckConfigure(configure uint32) {
	var msg [12]byte
	binary.LittleEndian.PutUint32(msg[:4], s.xdgSurface)
	binary.LittleEndian.PutUint16(msg[4:], waylandXdgSurfaceAckConfigureOpcode)
	binary.LittleEndian.PutUint16(msg[6:], waylandHeaderSize+4)
	binary.LittleEndian.PutUint32(msg[8:], configure)

	_, _ = unix.Write(s.fd, msg[:])
	slog.Debug("<- xdg_surface@ack_configure", "xdg_surface", s.xdgSurface, "configure", configure)

	s.canAttach.Store(true)
}

func (s *state) waylandXDGToplevelResize(serial uint32, edges uint32) {
	var msg [20]byte

	binary.LittleEndian.PutUint32(msg[:], s.xdgToplevel)
	binary.LittleEndian.PutUint16(msg[4:], waylandXDGToplevelResizeOpcode)
	binary.LittleEndian.PutUint16(msg[6:], waylandHeaderSize+12)

	binary.LittleEndian.PutUint32(msg[8:], s.wlSeat)
	binary.LittleEndian.PutUint32(msg[12:], serial)
	binary.LittleEndian.PutUint32(msg[16:], edges)

	_, _ = unix.Write(s.fd, msg[:])
	slog.Debug("<- xdg_toplevel@resize", "toplevel", s.xdgToplevel, "seat", s.wlSeat, "serial", serial, "edges", edges)
}

func (s *state) waylandWLCompositorCreateSurface() uint32 {
	var msg [12]byte
	binary.LittleEndian.PutUint32(msg[:4], s.wlCompositor)
	binary.LittleEndian.PutUint16(msg[4:], waylandWLCompositorCreateSurfaceOpcode)
	binary.LittleEndian.PutUint16(msg[6:], waylandHeaderSize+4)

	id := atomic.AddUint32(&s.currentId, 1)
	binary.LittleEndian.PutUint32(msg[8:], id)

	_, _ = unix.Write(s.fd, msg[:])
	slog.Debug("<- wl_compositor@create_surface", "compositor", s.wlCompositor, "surface", id)
	return id
}

func (s *state) waylandXdgWMBaseGetXdgSurface(surface uint32) uint32 {
	var msg [16]byte

	binary.LittleEndian.PutUint32(msg[:], s.xdgWMBase)
	binary.LittleEndian.PutUint16(msg[4:], waylandXdgWMBaseGetXdgSurfaceOpcode)
	binary.LittleEndian.PutUint16(msg[6:], waylandHeaderSize+4+4)

	id := atomic.AddUint32(&s.currentId, 1)
	binary.LittleEndian.PutUint32(msg[8:], id)
	binary.LittleEndian.PutUint32(msg[12:], surface)

	_, _ = unix.Write(s.fd, msg[:])
	slog.Debug("<- xdg_wm_base@get_xdg_surface", "wm", s.xdgWMBase, "surface", id, "wlSurface", s.wlSurface)
	return id
}

func (s *state) waylandXDGSurfaceGetToplevel() uint32 {
	var msg [12]byte

	binary.LittleEndian.PutUint32(msg[:], s.xdgSurface)
	binary.LittleEndian.PutUint16(msg[4:], waylandXdgSurfaceGetToplevelOpcode)
	binary.LittleEndian.PutUint16(msg[6:], waylandHeaderSize+4)

	id := atomic.AddUint32(&s.currentId, 1)
	binary.LittleEndian.PutUint32(msg[8:], id)

	_, _ = unix.Write(s.fd, msg[:])
	slog.Debug("<- xdg_surface@get_toplevel", "surface", s.xdgSurface, "toplevel", id)
	return id
}

func (s *state) waylandXDGToplevelSetAppID(appID string) {
	appIDBytes := []byte(appID)
	appIDLen := uint32(len(appIDBytes))
	strLen := appIDLen + 1 // + \0x00
	paddedLen := roundup4(strLen)

	if paddedLen+12 > 256 {
		panic("appId too long")
	}
	var msg [265]byte
	off := 0

	binary.LittleEndian.PutUint32(msg[off:], s.xdgToplevel)
	off += 4
	const xdgToplevelSetAppId = 3
	binary.LittleEndian.PutUint16(msg[off:], xdgToplevelSetAppId)
	off += 2
	binary.LittleEndian.PutUint16(msg[off:], waylandHeaderSize+uint16(4+paddedLen))
	off += 2

	// string length field (includes NUL terminator)
	binary.LittleEndian.PutUint32(msg[off:], strLen)
	off += 4

	copy(msg[off:], appIDBytes)
	off += int(appIDLen)
	off++
	for off%4 != 0 {
		off++
	}

	_, _ = unix.Write(s.fd, msg[:off])
	slog.Debug("<- xdg_toplevel@set_app_id", "toplevel", s.xdgToplevel, "appID", appID)
}

func (s *state) waylandXDGToplevelSetTitle(title string) {
	titleBytes := []byte(title)
	titleLen := uint32(len(titleBytes))
	strLen := titleLen + 1 // wire string length includes the NUL terminator
	paddedLen := roundup4(strLen)

	if paddedLen+12 > 256 {
		panic("title too long")
	}

	var msg [256]byte
	off := 0

	binary.LittleEndian.PutUint32(msg[off:], s.xdgToplevel)
	off += 4
	const xdgToplevelSetTitle = 2
	binary.LittleEndian.PutUint16(msg[off:], xdgToplevelSetTitle)
	off += 2
	binary.LittleEndian.PutUint16(msg[off:], waylandHeaderSize+4+uint16(paddedLen))
	off += 2

	// string length field (includes NUL terminator)
	binary.LittleEndian.PutUint32(msg[off:], strLen)
	off += 4

	copy(msg[off:], titleBytes)
	off += int(titleLen)
	//msg[off] = 0 // NUL terminator
	off++
	for off%4 != 0 {
		off++
	}

	_, _ = unix.Write(s.fd, msg[:off])
	slog.Debug("<- xdg_toplevel@set_title", "toplevel", s.xdgToplevel, "title", title)
}

func (s *state) waylandZxdgDecorationManagerGetToplevelDecoration() uint32 {
	var msg [16]byte

	binary.LittleEndian.PutUint32(msg[:], s.zxdgDecoManager)
	binary.LittleEndian.PutUint16(msg[4:], waylandZxdgDecorationManagerGetToplevelDecorationOpcode)
	binary.LittleEndian.PutUint16(msg[6:], waylandHeaderSize+4+4)

	id := atomic.AddUint32(&s.currentId, 1)
	binary.LittleEndian.PutUint32(msg[8:], id)
	binary.LittleEndian.PutUint32(msg[12:], s.xdgToplevel)

	_, _ = unix.Write(s.fd, msg[:])
	slog.Debug("<- zxdg_decoration_manager@get_toplevel_decoration", "deco", s.zxdgDecoManager, "toplevel", s.xdgToplevel)
	return id
}

func (s *state) waylandZxdgToplevelDecorationSetMode(mode uint32) {
	var msg [12]byte

	binary.LittleEndian.PutUint32(msg[:], s.zxdgToplevelDecoration)
	binary.LittleEndian.PutUint16(msg[4:], waylandZxdgToplevelDecorationSetModeOpcode)
	binary.LittleEndian.PutUint16(msg[6:], waylandHeaderSize+4)
	binary.LittleEndian.PutUint32(msg[8:], mode)

	_, _ = unix.Write(s.fd, msg[:])
	slog.Debug("<- zxdg_toplevel_decoration@set_mode", "deco", s.zxdgToplevelDecoration, "mode", mode)
}

func (s *state) waylandWLSeatGetPointer() uint32 {
	var msg [12]byte

	binary.LittleEndian.PutUint32(msg[:], s.wlSeat)
	binary.LittleEndian.PutUint16(msg[4:], waylandWLSeatGetPointerOpcode)
	binary.LittleEndian.PutUint16(msg[6:], waylandHeaderSize+4)

	id := atomic.AddUint32(&s.currentId, 1)
	binary.LittleEndian.PutUint32(msg[8:], id)

	_, _ = unix.Write(s.fd, msg[:])
	slog.Debug("<- wl_seat@get_pointer", "wlSeat", s.wlSeat, "wlPointer", id)
	return id
}

func (s *state) getViewport() {
	if s.wlSurface == 0 || s.wpViewporter == 0 {
		slog.Error("wpViewporter or wlSurface is not initialized", "wpViewporter", s.wpViewporter, "wlSurface", s.wlSurface)
		return
	}

	var msg [16]byte
	binary.LittleEndian.PutUint32(msg[:], s.wpViewporter)
	const getViewport = 1
	binary.LittleEndian.PutUint16(msg[4:], getViewport)
	binary.LittleEndian.PutUint16(msg[6:], waylandHeaderSize+8)
	id := atomic.AddUint32(&s.currentId, 1)
	binary.LittleEndian.PutUint32(msg[8:], id)
	binary.LittleEndian.PutUint32(msg[12:], s.wlSurface)
	s.viewport = id
	_, _ = unix.Write(s.fd, msg[:])
	slog.Debug("<- wp_viewporter@get_viewport", "wpViewporter", s.wpViewporter, "viewport", s.viewport)
}

func (s *state) createWindow() {
	if s.wlCompositor != 0 && s.wlSHM != 0 && s.xdgWMBase != 0 && s.wlSurface == 0 {
		s.wlSurface = s.waylandWLCompositorCreateSurface()
		s.getViewport()
		s.xdgSurface = s.waylandXdgWMBaseGetXdgSurface(s.wlSurface)
		s.xdgToplevel = s.waylandXDGSurfaceGetToplevel()
		s.waylandXDGToplevelSetAppID("EmoteChamp")
		s.waylandXDGToplevelSetTitle("EmoteChamp")

		s.plasmaSurface = s.plasmaShellGetSurface(s.wlSurface)

		if settings.Taskbar {
			s.iconManagerSetIcon(s.xdgToplevel, s.iconInstant)
		} else {
			s.plasmaSurfaceSetSkipTaskbar(true)
		}

		s.zxdgToplevelDecoration = s.waylandZxdgDecorationManagerGetToplevelDecoration()
		if settings.Decorations {
			s.waylandZxdgToplevelDecorationSetMode(waylandDecoModeServerSide)
		} else {
			s.waylandZxdgToplevelDecorationSetMode(waylandDecoModeClientSide)
		}

		s.waylandWLSurfaceCommit()
	} else {
		panic("wayland: createWindow called before all globals were bound")
	}

	// Get a wl_pointer from the bound seat once it's available.
	if s.wlSeat != 0 && s.wlPointer == 0 {
		s.wlPointer = s.waylandWLSeatGetPointer()

		if s.cursorShapeManager != 0 {
			s.cursorShapeDevice = s.ShapeGetPointer(s.wlPointer)
		}

		if s.dataDeviceManager != 0 {
			s.dataDevice = s.dataDeviceGetDataDevice(s.wlSeat)
		}
	}
}

func (s *state) viewportSetSource(x, y, width, height Fixed) {
	if s.viewport == 0 {
		slog.Error("viewport@setSource: viewport isn't set", "viewport", s.viewport)
		return
	}

	var msg [24]byte
	binary.LittleEndian.PutUint32(msg[0:], s.viewport)
	const setSource = 1
	binary.LittleEndian.PutUint16(msg[4:], setSource)
	binary.LittleEndian.PutUint16(msg[6:], waylandHeaderSize+16)
	binary.LittleEndian.PutUint32(msg[8:], uint32(x))
	binary.LittleEndian.PutUint32(msg[12:], uint32(y))
	binary.LittleEndian.PutUint32(msg[16:], uint32(width))
	binary.LittleEndian.PutUint32(msg[20:], uint32(height))

	_, _ = unix.Write(s.fd, msg[:])
	slog.Debug("<- viewport@setSource", "viewport", s.viewport, "x", x, "y", y, "width", width, "height", height)
}

func (s *state) viewportSetDestination(width, height uint32) {
	if s.viewport == 0 {
		slog.Error("viewport@setDestination: viewport isn't set", "viewport", s.viewport)
		return
	}

	var msg [16]byte
	binary.LittleEndian.PutUint32(msg[0:], s.viewport)
	const setDestination = 2
	binary.LittleEndian.PutUint16(msg[4:], setDestination)
	binary.LittleEndian.PutUint16(msg[6:], waylandHeaderSize+8)
	binary.LittleEndian.PutUint32(msg[8:], width)
	binary.LittleEndian.PutUint32(msg[12:], height)

	_, _ = unix.Write(s.fd, msg[:])
	slog.Debug("<- viewport@setDestination", "viewport", s.viewport, "width", width, "height", height)
}

// ensureBuffer guarantees a wl_buffer matching the current s.w/s.h, carved
// out of a single persistent wl_shm_pool. Creating the pool (and passing the
// fd) happens exactly once; on resize we only grow the pool when needed and
// destroy + recreate the wl_buffer. This avoids re-sending a memfd over
// SCM_RIGHTS on every resize.
func (s *state) ensureBuffer(size uint32) {
	if s.wlSHMPool == 0 {
		s.shmPoolSize = size
		var err error
		s.shmFD, s.shmPoolData, err = createSharedMemoryFile(uint64(s.shmPoolSize))
		if err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "Failed to create shared memory: %v\n", err)
			os.Exit(1)
		}
		s.wlSHMPool = s.waylandWLShmCreatePool(s.shmPoolSize, int32(s.shmFD))
		s.wlBuffer = s.waylandWLShmPoolCreateBuffer(s.wlSHMPool, 0, s.w, s.h, s.stride, waylandFormatARGB8888)
		s.bufferW, s.bufferH = s.w, s.h

		s.waylandWLSurfaceAttach()
		return
	}

	if s.bufferW == s.w && s.bufferH == s.h && s.wlBuffer != 0 {
		return // buffer already matches the requested size
	}

	// Size changed: grow the backing file and the compositor-side pool if
	// needed, then carve a fresh buffer out of the same pool at offset 0.
	//need := s.h * s.stride
	if size > s.shmPoolSize {
		if err := unix.Ftruncate(s.shmFD, int64(size)); err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "Failed to grow shared memory: %v\n", err)
			os.Exit(1)
		}

		data, err := unix.Mremap(s.shmPoolData, int(size), unix.MREMAP_MAYMOVE)
		if err != nil {
			panic(err)
		}
		data[len(data)-1] = 0

		s.shmPoolData = data
		s.shmPoolSize = size
		s.waylandWLShmPoolResize()
	}

	if s.wlBuffer != 0 {
		s.waylandWLBufferDestroy()
	}
	s.wlBuffer = s.waylandWLShmPoolCreateBuffer(s.wlSHMPool, 0, s.w, s.h, s.stride, waylandFormatARGB8888)
	s.bufferW, s.bufferH = s.w, s.h

	s.waylandWLSurfaceAttach()
}

func createSharedMemoryFile(size uint64) (int, []byte, error) {
	// Generate random name
	r := rand.New(rand.NewSource(time.Now().UnixNano()))
	var name strings.Builder
	name.WriteString("/")
	for i := 1; i < 64; i++ {
		name.WriteString(string(rune('a' + r.Intn(26))))
	}

	fd, err := unix.MemfdCreate(name.String(), 0)
	if err != nil {
		return 0, nil, fmt.Errorf("memfd_create: %w", err)
	}

	if err := unix.Ftruncate(fd, int64(size)); err != nil {
		_ = unix.Close(fd)
		return 0, nil, fmt.Errorf("ftruncate: %w", err)
	}

	data, err := unix.Mmap(fd, 0, int(size), unix.PROT_READ|unix.PROT_WRITE, unix.MAP_SHARED)
	if err != nil {
		_ = unix.Close(fd)
		return 0, nil, fmt.Errorf("mmap: %w", err)
	}

	return fd, data, nil
}

func (s *state) waylandWLShmPoolResize() {
	var msg [12]byte

	binary.LittleEndian.PutUint32(msg[:], s.wlSHMPool)
	binary.LittleEndian.PutUint16(msg[4:], waylandWLShmPoolResizeOpcode)
	binary.LittleEndian.PutUint16(msg[6:], waylandHeaderSize+4)

	binary.LittleEndian.PutUint32(msg[8:], s.shmPoolSize)

	_, _ = unix.Write(s.fd, msg[:])
	slog.Debug("<- wl_shm_pool@resize", "pool", s.wlSHMPool, "size", s.shmPoolSize)
}

func (s *state) waylandWLBufferDestroy() {
	var msg [8]byte

	binary.LittleEndian.PutUint32(msg[:], s.wlBuffer)
	binary.LittleEndian.PutUint16(msg[4:], waylandWLBufferDestroyOpcode)
	binary.LittleEndian.PutUint16(msg[6:], waylandHeaderSize)

	_, _ = unix.Write(s.fd, msg[:])
	slog.Debug("<- wl_buffer@destroy", "buffer", s.wlBuffer)

	s.wlBuffer = 0
}

func (s *state) waylandWLShmPoolCreateBuffer(pool, offset, width, height, stride, format uint32) uint32 {
	var msg [32]byte

	binary.LittleEndian.PutUint32(msg[0:], pool)
	binary.LittleEndian.PutUint16(msg[4:], waylandWLShmPoolCreateBufferOpcode)
	binary.LittleEndian.PutUint16(msg[6:], waylandHeaderSize+4+4*5)

	id := atomic.AddUint32(&s.currentId, 1)
	binary.LittleEndian.PutUint32(msg[8:], id)
	binary.LittleEndian.PutUint32(msg[12:], offset) // offset
	binary.LittleEndian.PutUint32(msg[16:], width)
	binary.LittleEndian.PutUint32(msg[20:], height)
	binary.LittleEndian.PutUint32(msg[24:], stride)
	binary.LittleEndian.PutUint32(msg[28:], format)

	_, _ = unix.Write(s.fd, msg[:])
	slog.Debug("<- wl_shm_pool@create_buffer", "pool", pool, "buffer", id)
	return id
}

func (s *state) waylandWLShmCreatePool(size uint32, fd int32) uint32 {
	var msg [16]byte

	binary.LittleEndian.PutUint32(msg[:], s.wlSHM)
	binary.LittleEndian.PutUint16(msg[4:], waylandWLShmCreatePoolOpcode)
	binary.LittleEndian.PutUint16(msg[6:], waylandHeaderSize+4+4)

	id := atomic.AddUint32(&s.currentId, 1)
	binary.LittleEndian.PutUint32(msg[8:], id)
	binary.LittleEndian.PutUint32(msg[12:], size)

	// Send file descriptor as ancillary data via sendmsg.
	// Build oob (out-of-band) control buffer: cmsghdr + fd payload.
	// Cmsghdr: Len(uint64) + Level(uint64) + Type(uint64) = 16 bytes.
	// Payload: 4 bytes for the fd, padded to 8.
	// Total: CmsgSpace(4) = 24 bytes.
	oobLen := unix.CmsgLen(4)
	oob := make([]byte, oobLen)
	cmsg := (*unix.Cmsghdr)(unsafe.Pointer(&oob[0]))
	cmsg.Len = uint64(oobLen)
	cmsg.Level = unix.SOL_SOCKET
	cmsg.Type = 1 // SCM_RIGHTS on Linux
	//fd32 := int32(s.shmFD)
	fd32 := fd
	copy(oob[unsafe.Sizeof(*cmsg):], (*(*[4]byte)(unsafe.Pointer(&fd32)))[:])

	_, err := unix.SendmsgN(s.fd, msg[:], oob, nil, 0)
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "sendmsg error: %v\n", err)
		os.Exit(1)
	}
	slog.Debug("<- wl_shm@create_pool", "shm", s.wlSHM, "pool", id)
	return id
}

func (s *state) waylandWLSurfaceDestroy() {
	if s.wlSurface == 0 {
		log.Println("wlSurface already destroyed")
		return
	}
	var msg [8]byte

	binary.LittleEndian.PutUint32(msg[:], s.wlSurface)
	const destroy = 0
	binary.LittleEndian.PutUint16(msg[4:], destroy)
	binary.LittleEndian.PutUint16(msg[6:], waylandHeaderSize)

	_, _ = unix.Write(s.fd, msg[:])
	slog.Debug("<- wl_surface@destroy", "surface", s.wlSurface)
	s.wlSurface = 0
}

func (s *state) waylandDestroyWindow() {
	if settings.content.cancel != nil {
		settings.content.cancel()
	}
	settings.content.name = ""
	s.cursorShapeDevice = 0
	s.waylandWLSurfaceDestroy()
	s.canAttach.Store(false)
}

func (s *state) iconManagerCreateIcon() {
	var msg [12]byte

	binary.LittleEndian.PutUint32(msg[:], s.iconManager)
	const createIcon = 1
	binary.LittleEndian.PutUint16(msg[4:], createIcon)
	binary.LittleEndian.PutUint16(msg[6:], uint16(waylandHeaderSize)+4)

	id := atomic.AddUint32(&s.currentId, 1)
	binary.LittleEndian.PutUint32(msg[8:], id)

	_, _ = unix.Write(s.fd, msg[:])
	slog.Debug("<- xdg_icon_manager_v1@create_icon", "icon_manager", s.iconManager, "icon", id)
	s.iconInstant = id
}

func (s *state) iconManagerSetIcon(xdgToplevel, icon uint32) {
	var msg [16]byte

	binary.LittleEndian.PutUint32(msg[:], s.iconManager)
	const setIcon = 2
	binary.LittleEndian.PutUint16(msg[4:], setIcon)
	binary.LittleEndian.PutUint16(msg[6:], uint16(waylandHeaderSize)+8)

	binary.LittleEndian.PutUint32(msg[8:], xdgToplevel)
	binary.LittleEndian.PutUint32(msg[12:], icon)

	_, _ = unix.Write(s.fd, msg[:])
	slog.Debug("<- xdg_icon_manager_v1@set_icon", "icon_manager", s.iconManager, "xdg_toplevel", xdgToplevel, "icon", icon)
}

func (s *state) iconAddBuffer(buffer, scale uint32) {
	var msg [16]byte

	binary.LittleEndian.PutUint32(msg[:], s.iconInstant)
	const addBuffer = 2
	binary.LittleEndian.PutUint16(msg[4:], addBuffer)
	binary.LittleEndian.PutUint16(msg[6:], uint16(waylandHeaderSize)+8)

	binary.LittleEndian.PutUint32(msg[8:], buffer)
	binary.LittleEndian.PutUint32(msg[12:], scale)

	_, _ = unix.Write(s.fd, msg[:])
	slog.Debug("<- xdg_icon_manager_v1@add_buffer", "icon", s.iconInstant, "buffer", buffer, "scale", scale)
}

func (s *state) setupIcon() {
	if s.iconInstant == 0 {
		s.iconManagerCreateIcon()
	}

	img := loadImage(settings.Icon)

	totalsize := uint32(0)
	for _, size := range s.IconSizes {
		totalsize += size * size * 4
	}

	fd, data, err := createSharedMemoryFile(uint64(totalsize))
	if err != nil {
		slog.Error("Failed to create shared memory", "err", err)
		return
	}
	pool := s.waylandWLShmCreatePool(totalsize, int32(fd))

	s.IconFd = fd
	s.iconMap = data
	s.iconPool = pool

	offset := uint32(0)
	for _, size := range s.IconSizes {
		buffer := s.waylandWLShmPoolCreateBuffer(pool, offset, size, size, size*4, waylandFormatARGB8888)
		img2 := WayImage{
			data: data[offset : offset+size*size*4],
			size: int(size),
		}

		draw2.CatmullRom.Scale(&img2, img2.Bounds(), img, img.Bounds(), draw.Src, nil)

		s.iconAddBuffer(buffer, size)
		offset += size * size * 4
	}
}

func (s *state) switchIcon() {
	oldIconId := s.iconInstant
	s.iconInstant = 0
	oldFd := s.IconFd
	oldMap := s.iconMap
	oldPool := s.iconPool

	s.setupIcon()

	if s.xdgToplevel != 0 {
		s.iconManagerSetIcon(s.xdgToplevel, s.iconInstant)
	}

	if oldIconId != 0 {
		s.iconManagerDestroyIcon(oldIconId)
	}
	if oldPool != 0 {
		s.waylandWLShmPoolDestroy(oldPool)
	}

	if oldMap != nil {
		err := syscall.Munmap(oldMap)
		if err != nil {
			slog.Error("failed to munmap old icon map", "err", err)
		}
	}

	if oldFd != 0 {
		err := syscall.Close(oldFd)
		if err != nil {
			slog.Error("failed to close old icon fd", "err", err)
		}
	}
}

func (s *state) iconManagerDestroyIcon(iconInstant uint32) {
	var msg [8]byte
	binary.LittleEndian.PutUint32(msg[:4], iconInstant)
	const destroyIcon = 0
	binary.LittleEndian.PutUint16(msg[4:6], destroyIcon)
	binary.LittleEndian.PutUint16(msg[6:8], waylandHeaderSize)

	_, _ = unix.Write(s.fd, msg[:])
	slog.Debug("iconManagerDestroyIcon", "iconInstant", iconInstant)
}

func (s *state) waylandWLShmPoolDestroy(pool uint32) {
	var msg [waylandHeaderSize]byte

	binary.LittleEndian.PutUint32(msg[:4], pool)
	const destroyShmBuffer = 1
	binary.LittleEndian.PutUint16(msg[4:6], destroyShmBuffer)
	binary.LittleEndian.PutUint16(msg[6:8], waylandHeaderSize)

	_, _ = unix.Write(s.fd, msg[:])
	slog.Debug("waylandWLShmPoolDestroy", "pool", pool)
}

type WayImage struct {
	data []byte
	size int
}

func (w *WayImage) ColorModel() color.Model {
	return color.RGBAModel
}

func (w *WayImage) Bounds() image.Rectangle {
	return image.Rect(0, 0, w.size, w.size)
}

func (w *WayImage) At(x, y int) color.Color {
	return color.RGBA{
		A: w.data[(y*w.size+x)*4+3],
		R: w.data[(y*w.size+x)*4+2],
		G: w.data[(y*w.size+x)*4+1],
		B: w.data[(y*w.size+x)*4+0],
	}
}

func (w *WayImage) Set(x, y int, c color.Color) {
	R, G, B, A := c.RGBA()
	w.data[(y*w.size+x)*4+3] = uint8(A >> 8)
	w.data[(y*w.size+x)*4+2] = uint8(R >> 8)
	w.data[(y*w.size+x)*4+1] = uint8(G >> 8)
	w.data[(y*w.size+x)*4+0] = uint8(B >> 8)
}

func loadImage(filename string) image.Image {
	var f fs.File
	var err error
	if path.Dir(filename) == "templates" {
		f, err = embedded.Open(filename)
	} else {
		f, err = os.Open(filename)
	}
	if err != nil {
		panic(err)
	}
	defer LogFailedClose(f.Close)

	img, _, err := image.Decode(f)
	if err != nil {
		panic(err)
	}

	return img
}

func (s *state) plasmaShellGetSurface(wlSurface uint32) (surfaceId uint32) {
	if s.plasmaShell == 0 {
		log.Println("plasmaShellGetSurface called but plasmaShell is not initialized")
		return
	}
	var msg [16]byte

	binary.LittleEndian.PutUint32(msg[:4], s.plasmaShell)
	const PlasmaShellGetSurface = 0
	binary.LittleEndian.PutUint16(msg[4:6], PlasmaShellGetSurface)
	binary.LittleEndian.PutUint16(msg[6:8], waylandHeaderSize+8)

	surfaceId = atomic.AddUint32(&s.currentId, 1)

	binary.LittleEndian.PutUint32(msg[8:12], surfaceId)
	binary.LittleEndian.PutUint32(msg[12:16], wlSurface)

	_, _ = unix.Write(s.fd, msg[:])

	slog.Debug("plasmaShellGetSurface", "surfaceId", surfaceId, "wlSurface", wlSurface)

	return surfaceId
}

func (s *state) plasmaSurfaceSetSkipTaskbar(skip bool) {
	if s.plasmaSurface == 0 {
		return
	}

	var msg [8 + 4]byte

	binary.LittleEndian.PutUint32(msg[:4], s.plasmaSurface)
	const PlasmaSurfaceSetSkipTaskbar = 5
	binary.LittleEndian.PutUint16(msg[4:6], PlasmaSurfaceSetSkipTaskbar)
	binary.LittleEndian.PutUint16(msg[6:8], waylandHeaderSize+4)

	skipVal := uint32(0)
	if skip {
		skipVal = 1
	}

	binary.LittleEndian.PutUint32(msg[8:12], skipVal)
	_, _ = unix.Write(s.fd, msg[:])
	slog.Debug("plasmaSurfaceSetSkipTaskbar", "plasmaSurface", s.plasmaSurface, "skip", skip)
}

func (s *state) ShapeGetPointer(pointer uint32) uint32 {
	if s.cursorShapeManager == 0 {
		panic("cursorShapeManager is not initialized")
		return 0
	}

	var msg [16]byte

	binary.LittleEndian.PutUint32(msg[:], s.cursorShapeManager)
	const getPointer = 1
	binary.LittleEndian.PutUint16(msg[4:6], getPointer)
	binary.LittleEndian.PutUint16(msg[6:8], waylandHeaderSize+8)
	cursorId := atomic.AddUint32(&s.currentId, 1)
	binary.LittleEndian.PutUint32(msg[8:12], cursorId)
	binary.LittleEndian.PutUint32(msg[12:16], pointer)

	_, _ = unix.Write(s.fd, msg[:])
	slog.Debug("wp_cursor_shape_manager_v1@get_pointer", "cursorId", cursorId, "pointer", pointer)

	return cursorId
}

func (s *state) ShapeSetCursor(serial uint32, cursor Cursor) {
	if s.cursorShapeDevice == 0 {
		panic("cursorShapeDevice is not initialized")
	}
	var msg [16]byte

	binary.LittleEndian.PutUint32(msg[:], s.cursorShapeDevice)
	const setShape = 1
	binary.LittleEndian.PutUint16(msg[4:6], setShape)
	binary.LittleEndian.PutUint16(msg[6:8], waylandHeaderSize+8)
	binary.LittleEndian.PutUint32(msg[8:12], serial)
	binary.LittleEndian.PutUint32(msg[12:16], uint32(cursor))
	_, _ = unix.Write(s.fd, msg[:])
	slog.Debug("<- wp_cursor_shape_manager_v1@set_cursor", "serial", serial, "cursor", cursor)
}

func (s *state) dataDeviceGetDataDevice(seat uint32) uint32 {
	var msg [16]byte

	binary.LittleEndian.PutUint32(msg[:], s.dataDeviceManager)
	const getDataDevice = 1
	binary.LittleEndian.PutUint16(msg[4:6], getDataDevice)
	binary.LittleEndian.PutUint16(msg[6:8], waylandHeaderSize+8)
	id := atomic.AddUint32(&s.currentId, 1)
	binary.LittleEndian.PutUint32(msg[8:], id)
	binary.LittleEndian.PutUint32(msg[12:], seat)

	_, _ = unix.Write(s.fd, msg[:])
	slog.Debug("<- wl_data_device_manager@get_data_device", "id", id, "seat", seat)
	return id
}

func (s *state) dataOfferAccept(serial uint32, mimeType []byte) {
	var msg [256]byte

	mimeTypeLen := uint32(len(mimeType)) + 1
	mimeLen := uint16(roundup4(mimeTypeLen))

	binary.LittleEndian.PutUint32(msg[:], s.dataOffer)
	const accept = 0
	binary.LittleEndian.PutUint16(msg[4:], accept)

	binary.LittleEndian.PutUint16(msg[6:], waylandHeaderSize+8+mimeLen)
	binary.LittleEndian.PutUint32(msg[8:], serial)
	binary.LittleEndian.PutUint32(msg[12:], mimeTypeLen)
	copy(msg[16:], mimeType)

	_, _ = unix.Write(s.fd, msg[:16+mimeLen])

	slog.Debug("<- data_offer@accept", "offer", s.dataOffer, "serial", serial, "mimeType", mimeType)
}

func (s *state) dataOfferReceive(mimeType []byte) {

	var fds [2]int // read, write
	err := unix.Pipe2(fds[:], unix.O_CLOEXEC)
	if err != nil {
		panic(err)
	}

	var msg [256]byte

	mimeTypeLen := uint32(len(mimeType)) + 1
	mimeLen := uint16(roundup4(mimeTypeLen))

	binary.LittleEndian.PutUint32(msg[:], s.dataOffer)
	const receive = 1
	binary.LittleEndian.PutUint16(msg[4:], receive)

	binary.LittleEndian.PutUint16(msg[6:], waylandHeaderSize+4+mimeLen)
	binary.LittleEndian.PutUint32(msg[8:], mimeTypeLen)
	copy(msg[12:], mimeType)

	oob := unix.UnixRights(fds[1])

	_, err = unix.SendmsgN(s.fd, msg[:12+mimeLen], oob, nil, 0)
	unix.Close(fds[1])
	if err != nil {
		unix.Close(fds[0])
		_, _ = fmt.Fprintf(os.Stderr, "sendmsg error: %v\n", err)
		os.Exit(1)
	}

	slog.Debug("<- data_offer@receive", "offer", s.dataOffer, "mimeType", mimeType)

	// TODO use in a meaningfully manner
	go func(readFD int) {
		pipeRead := os.NewFile(uintptr(readFD), "wayland-read-pipe")
		defer pipeRead.Close()

		mime := strings.ReplaceAll(string(mimeType), "/", "_")

		f, err := os.CreateTemp(tmpDir, mime+"-")
		if err != nil {
			slog.Error("failed to create temp file", "error", err)
			return
		}
		defer f.Close()

		fmt.Println("Writing selection to:", f.Name())

		written, err := io.Copy(f, pipeRead)
		if err != nil {
			slog.Error("pipe copy error", "error", err)
			return
		}

		fmt.Printf("[%s] Finished reading offer (%d bytes).\n", f.Name(), written)
	}(fds[0])
}

var tmpDir = func() string {
	s, err := os.MkdirTemp("", "emotechamp-*")
	if err != nil {
		panic(err)
	}
	return s
}()
