package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
)

const desAlphabet = "./0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"

var desEncoding = base64.NewEncoding(desAlphabet).WithPadding(base64.NoPadding)

var ErrMissingSiteSecret = errors.New("site is missing a site secret")
var ErrInvalidTripPassword = errors.New("password cannot be made a tripcode")

func regularTrip(password string) (string, error) {
	if password == "" {
		return "", ErrInvalidTripPassword
	}
	s := sha256.Sum256([]byte(password))
	tripcode := desAlphabetEncode(s[:])[:10]
	return tripcode, nil
}

func secureTrip(password, siteSecret string) (string, error) {
	if siteSecret == "" {
		return "", ErrMissingSiteSecret
	}
	if password == "" {
		return "", ErrInvalidTripPassword
	}
	mac := hmac.New(sha256.New, []byte(siteSecret))
	mac.Write([]byte(password))
	sum := mac.Sum(nil)
	return desEncoding.EncodeToString(sum)[:10], nil
}

func desAlphabetEncode(b []byte) string {
	return desEncoding.EncodeToString(b)
}
