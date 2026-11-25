//go:build !linux && !darwin

package pane

import "os"

func foregroundPgrp(*os.File) (int, error) { return 0, os.ErrNotExist }

func processName(int) string { return "" }

func cwdOf(int) string { return "" }
