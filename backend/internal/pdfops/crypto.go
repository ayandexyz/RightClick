package pdfops

import (
	"context"
	"errors"
	"fmt"

	"github.com/pdfcpu/pdfcpu/pkg/api"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
)

// EncryptParams holds parameters for pdf-encrypt operation.
type EncryptParams struct {
	User     string // user password (optional, can be empty)
	Owner    string // owner password (required)
	Password string // password for already-encrypted input
	// Permissions lists what a reader without the owner password may do. Nil
	// means everything: a password-protect that also forbade printing and
	// copying would surprise someone who only wanted the document locked.
	Permissions []string
}

// PermissionsParams holds parameters for pdf-permissions operation.
type PermissionsParams struct {
	Owner       string   // owner password (required)
	Permissions []string // allowed permissions: print, copy, modify, annotate
	Password    string   // password for already-encrypted input
}

// Validate checks that EncryptParams has required fields.
// Per spike results: pdfcpu requires owner password; user password is optional.
func (p EncryptParams) Validate() error {
	if p.Owner == "" {
		return errors.New("owner password is required")
	}
	return validatePermissionNames(p.Permissions)
}

// Validate checks that PermissionsParams has required fields and valid permissions.
func (p PermissionsParams) Validate() error {
	if p.Owner == "" {
		return errors.New("owner password is required")
	}

	return validatePermissionNames(p.Permissions)
}

// Decrypt removes password protection from a PDF.
// Requires the password in params.Password.
func (b *PDFCPU) Decrypt(ctx context.Context, input, output string, params Params) error {
	if params.Password == "" {
		return errors.New("password required to decrypt")
	}

	// Unlock to get decrypted copy
	unlocked, cleanup, err := Unlock(ctx, b, input, params.Password)
	if err != nil {
		return err
	}
	defer cleanup()

	// The unlocked copy is already decrypted, just copy it to output
	conf := model.NewDefaultConfiguration()
	return runPDFCPU(ctx, func() error {
		// Use Optimize as a way to copy the PDF without encryption
		return api.OptimizeFile(unlocked, output, conf)
	})
}

// Encrypt adds password protection to a PDF with AES-256 encryption.
func (b *PDFCPU) Encrypt(ctx context.Context, input, output string, params EncryptParams) error {
	if err := params.Validate(); err != nil {
		return err
	}

	// Check if input is already encrypted
	encrypted, err := isEncrypted(ctx, input)
	if err != nil {
		return err
	}

	workingPath := input
	var cleanup func()

	// If already encrypted, decrypt it first if password provided
	if encrypted {
		if params.Password == "" {
			return errors.New("input PDF is already encrypted; provide password to re-encrypt")
		}
		unlocked, cleanupFn, err := Unlock(ctx, b, input, params.Password)
		if err != nil {
			return fmt.Errorf("unlock for re-encryption: %w", err)
		}
		workingPath = unlocked
		cleanup = cleanupFn
		defer cleanup()
	}

	// Configure encryption with AES-256
	conf := model.NewDefaultConfiguration()
	conf.OwnerPW = params.Owner
	conf.UserPW = params.User // can be empty for owner-only
	conf.EncryptUsingAES = true
	conf.EncryptKeyLength = 256

	permissions := params.Permissions
	if permissions == nil {
		permissions = []string{"print", "copy", "modify", "annotate"}
	}
	conf.Permissions = permissionFlags(permissions)

	return runPDFCPU(ctx, func() error {
		return api.EncryptFile(workingPath, output, conf)
	})
}

// SetPermissions applies permission restrictions to a PDF.
func (b *PDFCPU) SetPermissions(ctx context.Context, input, output string, params PermissionsParams) error {
	if err := params.Validate(); err != nil {
		return err
	}

	// Check if input is already encrypted
	encrypted, err := isEncrypted(ctx, input)
	if err != nil {
		return err
	}

	workingPath := input
	var cleanup func()

	// If already encrypted, decrypt it first if password provided
	if encrypted {
		if params.Password == "" {
			return errors.New("input PDF is already encrypted; provide password to set permissions")
		}
		unlocked, cleanupFn, err := Unlock(ctx, b, input, params.Password)
		if err != nil {
			return fmt.Errorf("unlock for permissions: %w", err)
		}
		workingPath = unlocked
		cleanup = cleanupFn
		defer cleanup()
	}

	perms := permissionFlags(params.Permissions)

	// Configure encryption with owner password and permissions
	conf := model.NewDefaultConfiguration()
	conf.OwnerPW = params.Owner
	conf.UserPW = "" // Empty user password means open without password
	conf.EncryptUsingAES = true
	conf.EncryptKeyLength = 256
	conf.Permissions = perms

	return runPDFCPU(ctx, func() error {
		return api.EncryptFile(workingPath, output, conf)
	})
}

// permissionFlags turns the typed permission names into pdfcpu's flags. The
// names are validated before this is reached, so an unknown one is ignored.
func permissionFlags(names []string) model.PermissionFlags {
	var perms model.PermissionFlags = model.PermissionsNone
	for _, name := range names {
		switch name {
		case "print":
			perms |= model.PermissionsPrint
		case "copy":
			perms |= model.PermissionExtract
		case "modify":
			perms |= model.PermissionModify
		case "annotate":
			perms |= model.PermissionModAnnFillForm
		}
	}
	return perms
}

func validatePermissionNames(names []string) error {
	for _, name := range names {
		switch name {
		case "print", "copy", "modify", "annotate":
		default:
			return fmt.Errorf("invalid permission %q, must be one of: print, copy, modify, annotate", name)
		}
	}
	return nil
}
