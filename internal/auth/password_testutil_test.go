package auth

import "golang.org/x/crypto/bcrypt"

func bcryptHashForTest(pw string) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(pw), bcrypt.MinCost)
	if err != nil {
		return "", err
	}
	return string(b), nil
}
