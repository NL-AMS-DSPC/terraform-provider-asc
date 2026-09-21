package client

import "errors"

// ErrResourceNotFound indicates an authoritative 404 or absence from a successful
// collection read.
var ErrResourceNotFound = errors.New("resource not found")

// ErrResourceConflict is returned only for an authoritative HTTP 409.
var ErrResourceConflict = errors.New("resource conflict")
