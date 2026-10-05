package permissions

import (
	"fmt"
	"golang.org/x/sys/windows"
	"os"
)

func protectDir(path string) error {
	token := windows.GetCurrentProcessToken()
	user, err := token.GetTokenUser()
	if err != nil {
		return err
	}
	// Include the invoking account for console mode; services run as SYSTEM.
	dacl := "D:P(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)"
	if !token.IsElevated() {
		dacl += "(A;OICI;FA;;;" + user.User.Sid.String() + ")"
	}
	sd, err := windows.SecurityDescriptorFromString(dacl)
	if err != nil {
		return err
	}
	acl, _, err := sd.DACL()
	if err != nil {
		return err
	}
	flags := windows.SECURITY_INFORMATION(windows.DACL_SECURITY_INFORMATION | windows.PROTECTED_DACL_SECURITY_INFORMATION)
	var owner *windows.SID
	if token.IsElevated() {
		// Files created by an elevated interactive account can still be owned by
		// that account. Transfer ownership as well as protecting the DACL so the
		// SYSTEM service can trust them after installation.
		owner, err = windows.StringToSid("S-1-5-32-544")
		if err != nil {
			return err
		}
		flags |= windows.OWNER_SECURITY_INFORMATION
	}
	return windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, flags, owner, nil, acl, nil)
}
func ValidateConfigOwner(path string) error {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("configuration is not a regular file: %s", path)
	}
	return validateOwner(path)
}

func validateOwner(path string) error {
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION)
	if err != nil {
		return err
	}
	owner, _, err := sd.Owner()
	if err != nil {
		return err
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return err
	}
	if !trustedOwnerSID(owner.String(), user.User.Sid.String()) {
		return fmt.Errorf("untrusted configuration owner: %s", path)
	}
	return nil
}

func trustedOwnerSID(owner, currentUser string) bool {
	// Elevation does not change a process's user identity. In particular,
	// accepting an interactive administrator's own files must not mean a
	// SYSTEM service accepts files owned by that administrator's user SID.
	return owner == "S-1-5-18" || owner == "S-1-5-32-544" || owner == currentUser
}

func protectFile(path string) error { return protectDir(path) }

func EnsureServiceDir(path string) error {
	if !windows.GetCurrentProcessToken().IsElevated() {
		return fmt.Errorf("service installation requires Administrator permissions")
	}
	return EnsurePrivateDir(path)
}
