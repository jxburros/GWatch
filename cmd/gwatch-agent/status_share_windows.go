package main

import "golang.org/x/sys/windows"

// statusFileSDDL lets every signed-in user read the status file, and only the
// service and administrators change it. The directory it is in is private to
// SYSTEM and Administrators, but Windows grants everyone "bypass traverse
// checking" by default, so a file whose own ACL allows reading can be opened
// by its full path even when the folder around it cannot be listed. That is
// what lets the notification-area icon (tray_windows.go), which runs as the
// person signed in and not elevated, show what the service last did, while the
// configuration and the token beside it stay out of reach.
//
// "P" protects the list from the folder's inherited entries; the file holds
// no token and no readings (status.go), only how things are going.
const statusFileSDDL = "D:P(A;;FA;;;SY)(A;;FA;;;BA)(A;;FR;;;BU)"

// shareStatusFile applies statusFileSDDL to the status file before it is moved
// into place. A failure leaves the file as private as its folder, which only
// costs the tray its detail.
func shareStatusFile(path string) {
	sd, err := windows.SecurityDescriptorFromString(statusFileSDDL)
	if err != nil {
		return
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		return
	}
	_ = windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, dacl, nil)
}
