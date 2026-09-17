package client

import "errors"

// ErrResourceNotFound is returned only for an authoritative HTTP 404.
var ErrResourceNotFound = errors.New("resource not found")

// ErrResourceConflict is returned only for an authoritative HTTP 409.
var ErrResourceConflict = errors.New("resource conflict")
