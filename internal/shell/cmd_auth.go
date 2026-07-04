package shell

import (
	"github.com/ilioscio/alternate.sh/internal/db"
	"golang.org/x/crypto/bcrypt"
)

func cmdPasswd(s *Session, _ []string) error {
	rl := NewReadline(s.r, s.w)

	s.Print("Current password: ")
	current, err := readPassword(s.r, s.w)
	if err != nil {
		return nil
	}
	s.Write([]byte("\r\n"))

	if err := bcrypt.CompareHashAndPassword([]byte(s.User.PasswordHash), []byte(current)); err != nil {
		s.Println("passwd: incorrect password")
		return nil
	}

	s.Print("New password: ")
	newPass, err := readPassword(s.r, s.w)
	if err != nil {
		return nil
	}
	s.Write([]byte("\r\n"))

	s.Print("Confirm new password: ")
	confirm, err := readPassword(s.r, s.w)
	if err != nil {
		return nil
	}
	s.Write([]byte("\r\n"))

	_ = rl // silence unused warning

	if newPass != confirm {
		s.Println("passwd: passwords do not match")
		return nil
	}
	if len(newPass) < 8 {
		s.Println("passwd: password must be at least 8 characters")
		return nil
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(newPass), bcrypt.DefaultCost)
	if err != nil {
		s.Println("passwd: error hashing password")
		return nil
	}
	if err := db.UpdatePassword(s.ctx, s.db, s.User.ID, string(hash)); err != nil {
		s.Println("passwd: error saving password")
		return nil
	}
	s.User.PasswordHash = string(hash)
	s.Println("Password changed.")
	return nil
}

func cmdChfn(s *Session, _ []string) error {
	rl := NewReadline(s.r, s.w)

	s.Println("Change finger information. Press enter to keep current value.")
	s.Printf("Name [%s]: ", s.User.DisplayName)
	name, _ := rl.ReadLine("")
	if name == "" {
		name = s.User.DisplayName
	}

	s.Printf("Office [%s]: ", s.User.Office)
	office, _ := rl.ReadLine("")
	if office == "" {
		office = s.User.Office
	}

	s.Printf("Home phone [%s]: ", s.User.HomePhone)
	phone, _ := rl.ReadLine("")
	if phone == "" {
		phone = s.User.HomePhone
	}

	if err := db.UpdateFingerInfo(s.ctx, s.db, s.User.ID, name, office, phone); err != nil {
		s.Println("chfn: error saving finger info")
		return nil
	}
	s.User.DisplayName = name
	s.User.Office = office
	s.User.HomePhone = phone
	s.Println("Finger information updated.")
	return nil
}

// readPassword reads input without echoing characters.
func readPassword(r interface{ Read([]byte) (int, error) }, w interface{ Write([]byte) (int, error) }) (string, error) {
	var buf []byte
	b := make([]byte, 1)
	for {
		n, err := r.Read(b)
		if err != nil {
			return string(buf), err
		}
		if n == 0 {
			continue
		}
		switch b[0] {
		case '\r', '\n':
			return string(buf), nil
		case 0x7f, 0x08:
			if len(buf) > 0 {
				buf = buf[:len(buf)-1]
			}
		case 0x03:
			return "", nil
		default:
			if b[0] >= 0x20 {
				buf = append(buf, b[0])
			}
		}
	}
}
