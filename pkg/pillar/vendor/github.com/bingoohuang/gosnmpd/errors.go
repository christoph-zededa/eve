package gosnmpd

import "github.com/pkg/errors"

var (
	ErrUnsupportedProtoVersion = errors.New("ErrUnsupportedProtoVersion")
	ErrNoSNMPInstance          = errors.New("ErrNoSNMPInstance")
	ErrUnsupportedOperation    = errors.New("ErrUnsupportedOperation")
	ErrNoPermission            = errors.New("ErrNoPermission")
	ErrUnsupportedPacketData   = errors.New("ErrUnsupportedPacketData")
)
