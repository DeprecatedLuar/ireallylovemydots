// Package gitutil holds small helpers shared by every package that shells
// out to git. CappedWriter is the one piece worth sharing: a git command
// that can block on the network should stream to the user's terminal live
// rather than staying silent until it exits, while still giving callers
// enough of the output to build an error message on failure.
package gitutil

// tailBytes is how much of a git command's stderr CappedWriter keeps for
// error messages — enough for git's actual failure line, not the whole
// transcript of a long clone or fetch.
const tailBytes = 4096

// CappedWriter is an io.Writer that retains only the last tailBytes written
// to it. Pair it with io.MultiWriter(os.Stderr, &capped) as a git command's
// Stderr so output still streams live while capped.String() holds enough of
// the tail to report on failure.
type CappedWriter struct {
	buf []byte
}

func (c *CappedWriter) Write(p []byte) (int, error) {
	c.buf = append(c.buf, p...)
	if len(c.buf) > tailBytes {
		c.buf = c.buf[len(c.buf)-tailBytes:]
	}
	return len(p), nil
}

// String returns the captured tail.
func (c *CappedWriter) String() string {
	return string(c.buf)
}
