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
	return windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, acl, nil)
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
	sid := owner.String()
	if sid != "S-1-5-18" && sid != "S-1-5-32-544" && (sid != user.User.Sid.String() || windows.GetCurrentProcessToken().IsElevated()) {
		return fmt.Errorf("untrusted configuration owner: %s", path)
	}
	return nil
}

func protectFile(path string) error { return protectDir(path) }

func EnsureServiceDir(path string) error {
	if !windows.GetCurrentProcessToken().IsElevated() {
		return fmt.Errorf("service installation requires Administrator permissions")
	}
	return EnsurePrivateDir(path)
}
