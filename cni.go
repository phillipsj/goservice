package main

import "context"

type Cni interface {
	startCni(cniType string, errChan chan error, ctx context.Context)
}

