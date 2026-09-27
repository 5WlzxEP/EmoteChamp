package main

// Wayland protocol constants
const (
	waylandDisplayObjectID                                  uint32 = 1
	waylandWLRegistryEventGlobal                            uint16 = 0
	waylandWLRegistryEventGlobalRemove                      uint16 = 1
	waylandSHMPoolEventFormat                               uint16 = 0
	waylandWLBufferEventRelease                             uint16 = 0
	waylandXdgWMBaseEventPing                               uint16 = 0
	waylandXdgToplevelEventConfigure                        uint16 = 0
	waylandXdgToplevelEventClose                            uint16 = 1
	waylandXdgToplevelEventConfigureBounds                  uint16 = 2
	waylandXdgToplevelEventWMCapabilities                   uint16 = 3
	waylandXdgSurfaceEventConfigure                         uint16 = 0
	waylandWLSurfaceEventEnter                              uint16 = 0
	waylandWLSurfaceEventLeave                              uint16 = 1
	waylandWLSurfaceEventPreferredBufferScale               uint16 = 2
	waylandWLSurfaceEventPreferredBufferTransform           uint16 = 3
	waylandWLDisplayGetRegistryOpcode                       uint16 = 1
	waylandWLRegistryBindOpcode                             uint16 = 0
	waylandWLCompositorCreateSurfaceOpcode                  uint16 = 0
	waylandXdgWMBasePongOpcode                              uint16 = 3
	waylandXdgSurfaceAckConfigureOpcode                     uint16 = 4
	waylandWLShmCreatePoolOpcode                            uint16 = 0
	waylandXdgWMBaseGetXdgSurfaceOpcode                     uint16 = 2
	waylandWLShmPoolCreateBufferOpcode                      uint16 = 0
	waylandWLSurfaceAttachOpcode                            uint16 = 1
	waylandWLSurfaceDamageOpcode                            uint16 = 2
	waylandXdgSurfaceGetToplevelOpcode                      uint16 = 1
	waylandWLSurfaceCommitOpcode                            uint16 = 6
	waylandWLDisplayErrorEvent                              uint16 = 0
	waylandWLDisplayEventDeleteID                           uint16 = 1
	waylandWLBufferDestroyOpcode                            uint16 = 0
	waylandWLShmPoolResizeOpcode                            uint16 = 2
	waylandZxdgDecorationManagerGetToplevelDecorationOpcode uint16 = 1
	waylandZxdgToplevelDecorationSetModeOpcode              uint16 = 1
	waylandZxdgToplevelDecorationEventConfigure             uint16 = 0

	waylandFormatARGB8888 uint32 = 0
	waylandFormatXRGB8888 uint32 = 1

	waylandHeaderSize         uint16 = 8
	colorChannels             uint32 = 4
	waylandDecoModeNone       uint32 = 0
	waylandDecoModeClientSide uint32 = 1
	waylandDecoModeServerSide uint32 = 2
	// wl_seat requests
	waylandWLSeatGetPointerOpcode uint16 = 0
	// wl_pointer events
	waylandWLPointerEventEnter  uint16 = 0
	waylandWLPointerEventLeave  uint16 = 1
	waylandWLPointerEventMotion uint16 = 2
	waylandWLPointerEventButton uint16 = 3
	// xdg_toplevel requests
	waylandXDGToplevelResizeOpcode  uint16 = 6
	waylandXdgResizeEdgeBottomRight uint32 = 10
	// Pointer button 0x110 == BTN_LEFT
	waylandPointerButtonLeft uint32 = 0x110
	// Size (pixels) of the bottom-right resize grab area.
	resizeHandleSize int32 = 24
)

type Cursor uint32

const (
	defaultCursor        = 1  // default cursor
	contextMenucursor    = 2  // a context menu is available for the object under the cursor
	helpCursor           = 3  // help is available for the object under the cursor
	pointerCursor        = 4  // pointer that indicates a link or another interactive element
	progressCursor       = 5  // progress indicator
	waitCursor           = 6  // program is busy, user should wait
	cellCursor           = 7  // a cell or set of cells may be selected
	crosshairCursor      = 8  // simple crosshair
	textCursor           = 9  // text may be selected
	verticalTextcursor   = 10 // vertical text may be selected
	aliasCursor          = 11 // drag-and-drop: alias of/shortcut to something is to be created
	copyCursor           = 12 // drag-and-drop: something is to be copied
	moveCursor           = 13 // drag-and-drop: something is to be moved
	noDropcursor         = 14 // drag-and-drop: the dragged item cannot be dropped at the current cursor location
	notAllowedcursor     = 15 // drag-and-drop: the requested action will not be carried out
	grabCursor           = 16 // drag-and-drop: something can be grabbed
	grabbingCursor       = 17 // drag-and-drop: something is being grabbed
	eResizecursor        = 18 // resizing: the east border is to be moved
	nResizecursor        = 19 // resizing: the north border is to be moved
	neResizecursor       = 20 // resizing: the north-east corner is to be moved
	nwResizecursor       = 21 // resizing: the north-west corner is to be moved
	sResizecursor        = 22 // resizing: the south border is to be moved
	seResizecursor       = 23 // resizing: the south-east corner is to be moved
	swResizecursor       = 24 // resizing: the south-west corner is to be moved
	wResizecursor        = 25 // resizing: the west border is to be moved
	ewResizecursor       = 26 // resizing: the east and west borders are to be moved
	nsResizecursor       = 27 // resizing: the north and south borders are to be moved
	neswResizecursor     = 28 // resizing: the north-east and south-west corners are to be moved
	nwseResizecursor     = 29 // resizing: the north-west and south-east corners are to be moved
	colResizecursor      = 30 // resizing: that the item/column can be resized horizontally
	rowResizecursor      = 31 // resizing: that the item/row can be resized vertically
	allScrollcursor      = 32 // something can be scrolled in any direction
	zoomIncursor         = 33 // something can be zoomed in
	zoomOutcursor        = 34 // something can be zoomed out
	dndAsksincecursor    = 35 // drag-and-drop: the user will select which action will be carried out (non-css value)
	allResizesincecursor = 36 // resizing: something can be moved or resized in any direction (non-css value)
)
