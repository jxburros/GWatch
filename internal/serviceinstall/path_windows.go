package serviceinstall

import "golang.org/x/sys/windows"

func programFiles() (string, error) { return windows.KnownFolderPath(windows.FOLDERID_ProgramFiles, 0) }
