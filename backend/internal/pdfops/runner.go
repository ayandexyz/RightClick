package pdfops

import "context"

// Runner executes one external binary with the given arguments. It matches the
// seam the conversion engines use so tests can assert argument lists without
// the real tools installed.
type Runner func(ctx context.Context, name string, args []string) error
