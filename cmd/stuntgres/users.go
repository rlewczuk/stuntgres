package main

import (
	"bufio"
	"errors"
	"io"
	"os"
	"strings"
	"stuntgres/internal/serverauth"
)

func readPasswordPair(in *os.File, out io.Writer) (password string, err error) {
	input, restore, err := disablePasswordEcho(in)
	if err != nil {
		return "", err
	}
	defer func() {
		err = errors.Join(err, restore())
		if err != nil {
			password = ""
		}
	}()
	reader := bufio.NewReader(input)
	readLine := func(prompt string) (string, error) {
		if _, err := io.WriteString(out, prompt); err != nil {
			return "", err
		}
		line, err := reader.ReadString('\n')
		if err != nil && !(errors.Is(err, io.EOF) && len(line) != 0) {
			return "", err
		}
		line = strings.TrimSuffix(line, "\n")
		if _, err := io.WriteString(out, "\n"); err != nil {
			return "", err
		}
		return line, serverauth.ValidPassword(line)
	}
	first, err := readLine("Password: ")
	if err != nil {
		return "", err
	}
	second, err := readLine("Repeat password: ")
	if err != nil {
		return "", err
	}
	if first != second {
		return "", errors.New("passwords do not match")
	}
	return first, nil
}
