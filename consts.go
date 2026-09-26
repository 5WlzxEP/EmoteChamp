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
