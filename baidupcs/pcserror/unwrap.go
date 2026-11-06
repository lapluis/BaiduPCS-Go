package pcserror

// Unwrap preserves the underlying network, JSON, or internal error for errors.Is
// and errors.As, while Error retains the operation and remote error details.
func (pcse *PCSErrInfo) Unwrap() error { return pcse.Err }

// Unwrap returns the underlying error.
func (pane *PanErrorInfo) Unwrap() error { return pane.Err }

// Unwrap returns the underlying error.
func (pane *XPanErrorInfo) Unwrap() error { return pane.Err }

// Unwrap returns the underlying error.
func (dle *DlinkErrInfo) Unwrap() error { return dle.Err }
