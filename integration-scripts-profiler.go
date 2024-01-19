package main

import (
	"archive/zip"
	"bufio"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"

	"github.com/fatih/color"
)

func main() {
	// Colors used across the program.
	redBackground := color.New(color.BgRed).SprintFunc()
	redText := color.New(color.FgRed).SprintFunc()

	// For reading user input.
	reader := bufio.NewReader(os.Stdin)

	// Goodies.
	var defaultTMP string
	var inputHasBeenAccepted bool
	var schedulerSelected string

	// # Add some code that'll allow arrow keys to be used when prompted for user input.
	// # Add
	// Setup for better Ctrl+C messaging. This is a channel to receive OS signals.
	signalChan := make(chan os.Signal, 1)
	signal.Notify(signalChan, os.Interrupt, syscall.SIGTERM)

	// Start a goroutine to listen for signals.
	go func() {

		// Wait for the signal.
		<-signalChan

		// Handle the signal by exiting the program.
		fmt.Println(redBackground("\nExiting from user input..."))
		os.Exit(0)
	}()

	// Figure out your OS.
	switch userOS := runtime.GOOS; userOS {
	case "darwin":
		defaultTMP = "/tmp"
	case "windows":
		defaultTMP = os.Getenv("TMP")
	case "linux":
		defaultTMP = "/tmp"
	default:
		defaultTMP = "unknown"
		fmt.Println(redText("Your operating system is unrecognized. Exiting."))
		os.Exit(0)
	}

	// waaaahhhh it's too difficult to just say "while".
	for inputHasBeenAccepted == false {
		fmt.Print("Select the scheduler you'd like to use by entering its corresponding number.")
		schedulerSelected, _ = reader.ReadString('\n')
		schedulerSelected = strings.TrimSpace(schedulerSelected)
		fmt.Print(defaultTMP) // Putting this here for now so Go will shut the hell up.
	}

}

// Function to download a file from a given URL and save it to the specified path.
func downloadFile(url string, filePath string) error {
	response, err := http.Get(url)
	if err != nil {
		return err
	}
	defer response.Body.Close()

	file, err := os.Create(filePath)
	if err != nil {
		return err
	}
	defer file.Close()

	_, err = io.Copy(file, response.Body)
	if err != nil {
		return err
	}

	return nil
}

// Function to unzip MPM, since we have to on Windows and macOS.
func unzipFile(src, dest string) error {
	reader, err := zip.OpenReader(src)
	if err != nil {
		return err
	}
	defer reader.Close()

	for _, file := range reader.File {
		path := filepath.Join(dest, file.Name)

		// Reconstruct the file path on Windows to ensure proper subdirectories are created.
		// Don't know why other OSes don't need this.
		if runtime.GOOS == "windows" {
			path = filepath.Join(dest, file.Name)
			path = filepath.FromSlash(path)
		}

		if file.FileInfo().IsDir() {
			os.MkdirAll(path, file.Mode())
			continue
		}

		err := os.MkdirAll(filepath.Dir(path), 0755)
		if err != nil {
			return err
		}

		fileReader, err := file.Open()
		if err != nil {
			return err
		}
		defer fileReader.Close()

		targetFile, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, file.Mode())
		if err != nil {
			return err
		}
		defer targetFile.Close()

		_, err = io.Copy(targetFile, fileReader)
		if err != nil {
			return err
		}
	}

	return nil
}
