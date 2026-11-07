package webframe

import "errors"

// ErrSnapshotUnsupported is returned when the platform's WebView backend cannot
// rasterize the page (today: everything except macOS).
var ErrSnapshotUnsupported = errors.New("webframe: this backend cannot snapshot the page")

// snapshotter is the optional capability a WebView backend may implement.
// It is declared here, structurally, rather than imported from the binding:
// webframe then works with any version of the binding, with or without the
// capability, and the two can be reviewed independently.
type snapshotter interface {
	Snapshot() ([]byte, error)
}

// Snapshot returns the current page as PNG bytes.
//
// Frame keeps its WebView unexported, so this is the only way to a capture from
// outside the package. Call it from a goroutine that is not the UI thread: the
// capture completes on the main queue, which Run is pumping.
func (f *Frame) Snapshot() ([]byte, error) {
	if f == nil || f.w == nil {
		return nil, ErrSnapshotUnsupported
	}
	s, ok := f.w.(snapshotter)
	if !ok {
		return nil, ErrSnapshotUnsupported
	}
	return s.Snapshot()
}
