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
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"syscall"

	"github.com/chzyer/readline"
	"github.com/fatih/color"
)

type FolderCompleter struct {
	Folders []string
}

func (f *FolderCompleter) Do(line []rune, pos int) (newLine [][]rune, length int) {
	prefix := string(line[:pos]) // Ensure we're only considering the part of the line up to the cursor.
	for _, folder := range f.Folders {
		if strings.HasPrefix(folder, prefix) {
			// Append only the remaining part of the folder name beyond the prefix.
			remainingPart := folder[len(prefix):]
			newLine = append(newLine, []rune(remainingPart))
		}
	}
	length = len(prefix)
	return
}

func main() {
	// To handle keyboard input better.
	rl, err := readline.New("> ")
	if err != nil {
		panic(err)
	}
	defer rl.Close()

	// Colors used across the program.
	redBackground := color.New(color.BgRed).SprintFunc()
	redText := color.New(color.FgRed).SprintFunc()

	// Goodies.
	var input string
	var scriptsPath string
	var accessToken string
	var gitRepoURL string
	var downloadScriptsOnLanuch bool = true
	var useCaseNumber bool = true
	var caseNumber int
	var gitRepoPath string
	var schedulerSelected string
	var organizationSelected string
	var organizationAbbreviation string
	var organizationContact string
	var customMPIInput string
	var customMPI bool
	var clusterCount int
	var clusterName string
	var submissionType string
	var numberOfWorkers int
	var hasSharedFileSystem bool
	var clusterMatlabRoot string
	var clusterHostname string
	var remoteJobStorageLocation string

	// Setup for better Ctrl+C messaging. This is a channel to receive OS signals.
	signalChan := make(chan os.Signal, 1)
	signal.Notify(signalChan, os.Interrupt, syscall.SIGTERM)

	// Start a Goroutine to listen for signals.
	go func() {

		// Wait for the signal.
		<-signalChan

		// Handle the signal by exiting the program and reporting it as so.
		fmt.Print(redBackground("\nExiting from user input..."))
		os.Exit(0)
	}()

	// Regexp compile used for detecting things with numbers and letters.
	lettersAndNumbersPattern, err := regexp.Compile(`^[^a-zA-Z0-9]+$`)
	if err != nil {
		fmt.Println(redText("Error compiling regex pattern:", err, " Exiting."))
		os.Exit(0)
	}

	lettersPattern, err := regexp.Compile(`^[^a-zA-Z]+$`)
	if err != nil {
		fmt.Println(redText("Error compiling regex pattern:", err, " Exiting."))
		os.Exit(0)
	}

	// Determine your OS.
	switch userOS := runtime.GOOS; userOS {
	case "darwin":
		scriptsPath = "/tmp"
	case "windows":
		scriptsPath = os.Getenv("TMP")
	case "linux":
		scriptsPath = "/tmp"
	default:
		scriptsPath = "unknown"
		fmt.Print(redText("\nYour operating system is unrecognized. Exiting."))
		os.Exit(0)
	}

	// Determine any user-defined settings.
	currentDir, err := os.Getwd() // Get the current working directory.
	if err != nil {
		fmt.Print(redText("\nError getting current working directory while looking for user settings : ", err, " Default settings will be used instead."))
		return
	} else {
		settingsPath := filepath.Join(currentDir, "settings.txt")

		// Check if the settings file exists.
		if _, err := os.Stat(settingsPath); os.IsNotExist(err) {
			// No settings file found.
			return
		} else if err != nil {
			fmt.Print(redText("\nError checking for user settings: ", err, " Default settings will be used instead."))
		} else {
			fmt.Print("\nCustom settings found!")
			file, err := os.Open(settingsPath)
			if err != nil {
				fmt.Print(redText("\nError opening settings file: ", err, " Default settings will be used instead."))
				return
			}
			defer file.Close()

			scanner := bufio.NewScanner(file)

			for scanner.Scan() {
				line := scanner.Text()

				if !strings.HasPrefix(line, "#") {
					if strings.HasPrefix(strings.ToLower(line), "downloadscriptsonlaunch") {
						if strings.Contains(strings.ToLower(line), "false") {
							downloadScriptsOnLanuch = false
							fmt.Print("\nA new set of integration scripts will not be downloaded per your settings.")
						}

					} else if strings.HasPrefix(line, "scriptsPath =") || strings.HasPrefix(line, "scriptsPath=") {
						scriptsPath = strings.TrimPrefix(line, "scriptsPath =")
						scriptsPath = strings.TrimPrefix(scriptsPath, "scriptsPath=")
						scriptsPath = strings.TrimSpace(scriptsPath)
						scriptsPath = strings.Trim(scriptsPath, "\"")

						_, err := os.Stat(scriptsPath) // Do you actually exist? Does anything actually exist, man?
						if err != nil {
							fmt.Print(redText("\nThe custom scripts path you've specified, \"", scriptsPath, " does not exist. Please adjust your settings accordingly."))
							os.Exit(0)
						}

						if !downloadScriptsOnLanuch {
							schedulers := []string{"slurm", "pbs", "lsf", "gridengine", "htcondor", "awsbatch", "kubernetes"}
							for _, scheduler := range schedulers {
								schedulerDirectoryName := "matlab-parallel-" + scheduler + "-plugin-main"
								schedulerPath := filepath.Join(scriptsPath, schedulerDirectoryName)
								if _, err := os.Stat(schedulerPath); err != nil {
									fmt.Printf(redText("\nThe path you've specified is missing the needed integration scripts folder \"%s\".\n"), schedulerDirectoryName)
									os.Exit(0)
								}
							}
						}

						fmt.Print("\nA custom integration scripts download path has been set to ", scriptsPath)

					} else if strings.HasPrefix(line, "accessToken =") || strings.HasPrefix(line, "accessToken=") {
						accessToken = strings.TrimPrefix(line, "accessToken =")
						accessToken = strings.TrimPrefix(accessToken, "accessToken=")
						accessToken = strings.TrimSpace(accessToken)
						accessToken = strings.Trim(accessToken, "\"")
						fmt.Print("\nYour access token has been set to ", accessToken)
					} else if strings.HasPrefix(line, "gitRepoPath =") || strings.HasPrefix(line, "gitRepoPath=") {
						gitRepoPath = strings.TrimPrefix(line, "gitRepoPath =")
						gitRepoPath = strings.TrimPrefix(gitRepoPath, "gitRepoPath=")
						gitRepoPath = strings.TrimSpace(gitRepoPath)
						gitRepoPath = strings.Trim(gitRepoPath, "\"")

						// Check if the path exists.
						if _, err := os.Stat(gitRepoPath); os.IsNotExist(err) {
							fmt.Print("\nThe specified Git repo path does not exist:", gitRepoPath, ". It will not be used.")
							gitRepoPath = ""
						} else {
							fmt.Print("\nYour GitRepo path has been set to ", gitRepoPath)
						}
					} else if strings.HasPrefix(line, "gitRepoURL =") || strings.HasPrefix(line, "gitRepoURL=") {
						gitRepoURL = strings.TrimPrefix(line, "gitRepoURL =")
						gitRepoURL = strings.TrimPrefix(gitRepoURL, "gitRepoURL=")
						gitRepoURL = strings.TrimSpace(gitRepoURL)
						gitRepoURL = strings.Trim(gitRepoURL, "\"")
						fmt.Print("\nYour Git repo URL has been set to ", gitRepoURL)
					} else if strings.HasPrefix(strings.ToLower(line), "usecasenumber") {
						if strings.Contains(strings.ToLower(line), "false") {
							useCaseNumber = false
							fmt.Print("\nPer your settings, you will not be prompted to fill in a Case Number.")
						}
					} else {
						fmt.Print(redText("\nUnrecognized setting detected. The line in question has this content: ", line))
					}
				}
			}

			if err := scanner.Err(); err != nil {
				fmt.Print(redText("\nError reading settings file:", err, " Default settings will be used instead."))
			}
		}
	}

	if downloadScriptsOnLanuch {
		fmt.Print("\nBeginning download of integration scripts. Please wait.")

		var scriptsURLs = map[string]string{
			"https://codeload.github.com/mathworks/matlab-parallel-slurm-plugin/zip/refs/heads/main":      "slurm.zip",
			"https://codeload.github.com/mathworks/matlab-parallel-pbs-plugin/zip/refs/heads/main":        "pbs.zip",
			"https://codeload.github.com/mathworks/matlab-parallel-lsf-plugin/zip/refs/heads/main":        "lsf.zip",
			"https://codeload.github.com/mathworks/matlab-parallel-htcondor-plugin/zip/refs/heads/main":   "htcondor.zip",
			"https://codeload.github.com/mathworks/matlab-parallel-gridengine-plugin/zip/refs/heads/main": "gridengine.zip",
			"https://codeload.github.com/mathworks/matlab-parallel-awsbatch-plugin/zip/refs/heads/main":   "awsbatch.zip",
			"https://codeload.github.com/mathworks/matlab-parallel-kubernetes-plugin/zip/refs/heads/main": "kubernetes.zip",
		}

		for url, zipArchive := range scriptsURLs {
			zipArchivePath := filepath.Join(scriptsPath, zipArchive)
			err := downloadFile(url, zipArchivePath)
			if err != nil {
				fmt.Print(redText("\nFailed to download the integration scripts: ", err))
				continue
			}

			// Extract ZIP archives.
			schedulerName := strings.TrimSuffix(zipArchive, ".zip")
			unzipPath := filepath.Join(scriptsPath, schedulerName)

			// Check if the integration scripts directory already exists. Delete it if it is.
			if _, err := os.Stat(unzipPath); err == nil {

				err := os.RemoveAll(unzipPath)
				if err != nil {
					fmt.Print(redText("\nFailed to delete the existing integration scripts directory:", err))
					continue
				}
			}

			err = unzipFile(zipArchivePath, scriptsPath)
			if err != nil {
				fmt.Print(redText("\nFailed to extract integration scripts:", err))
				continue
			}

			if strings.Contains(zipArchivePath, "kubernetes.zip") {
				fmt.Print("\nLatest integration scripts downloaded and extracted successfully!")
			}
		}
	} else {
		fmt.Print("\nIntegration scripts download skipped per user's settings.")
	}

	// List existing engagements and setup auto-completion.
	var engagementFolders []string
	if gitRepoPath != "" {
		customerEngagementsPath := filepath.Join(gitRepoPath, "Customer-Engagements")
		if _, err := os.Stat(customerEngagementsPath); !os.IsNotExist(err) {
			files, err := os.ReadDir(customerEngagementsPath)
			if err != nil {
				fmt.Print(redText("\nError reading directory:", err))
			} else {
				fmt.Print("\n\nExisting engagements found:\n\n")
				for _, f := range files {
					if f.IsDir() {
						engagementFolders = append(engagementFolders, f.Name())
						fmt.Println("-", f.Name())
					}
				}
			}
		}
	}

	// Setup auto-completer.
	completer := &FolderCompleter{Folders: engagementFolders}
	rl.Config.AutoComplete = completer

	for {
		fmt.Print("\nEnter the organization's name.\n")
		organizationSelected, err = rl.Readline()
		if err != nil {
			if err.Error() == "Interrupt" {
				fmt.Println(redText("Exiting from user input."))
			} else {
				fmt.Print(redText("Error reading line:", err))
				continue
			}
			return
		}
		organizationSelected = strings.TrimSpace(organizationSelected)

		if organizationSelected == "" {
			fmt.Print(redText("Invalid entry. "))
			continue
		} else {
			break
		}
	}
	// # Add some code that'll check to see if the abbreviation has already been set in the remote git repo.
	for {
		fmt.Print("\nEnter the organization's abrreviation. If it's unknown, leave it empty.\n")
		organizationAbbreviation, err = rl.Readline()
		if err != nil {
			if err.Error() == "Interrupt" {
				fmt.Println(redText("Exiting from user input."))
			} else {
				fmt.Print(redText("Error reading line:", err))
				continue
			}
			return
		}
		organizationAbbreviation = strings.TrimSpace(organizationAbbreviation)

		if organizationAbbreviation == "" {
			break
		} else if lettersPattern.MatchString(organizationAbbreviation) && organizationAbbreviation != "" {
			fmt.Print(redText("\nInvalid input. You may only use letters in the abbreviation and at least 1 letter is required.\n"))
			continue
		} else {
			break
		}
	}

	if gitRepoPath != "" {

		// List existing contacts and setup auto-completion.
		var contactFolders []string
		if gitRepoPath != "" {
			contactsPath := filepath.Join(gitRepoPath, "Customer-Engagements", organizationSelected)
			if _, err := os.Stat(contactsPath); !os.IsNotExist(err) {
				files, err := os.ReadDir(contactsPath)
				if err != nil {
					fmt.Print(redText("\nError reading directory:", err))
				} else {
					fmt.Print("\n\nExisting contacts found:\n\n")
					for _, f := range files {
						if f.IsDir() {
							contactFolders = append(contactFolders, f.Name())
							fmt.Println("-", f.Name())
						}
					}
				}
			}
		}

		// Setup auto-completer.
		completer := &FolderCompleter{Folders: contactFolders}
		rl.Config.AutoComplete = completer

		for {
			fmt.Print("\nEnter the organization's contact name. If it's unknown, leave it empty and it will populate as \"first-last\".\n")
			organizationContact, err = rl.Readline()
			if err != nil {
				if err.Error() == "Interrupt" {
					fmt.Println(redText("Exiting from user input."))
				} else {
					fmt.Print(redText("Error reading line:", err))
					continue
				}
				return
			}

			organizationContact = strings.TrimSpace(organizationContact)

			if organizationContact == "" {
				organizationContact = "first-last"
				break
			} else if lettersPattern.MatchString(organizationContact) && organizationContact != "" {
				fmt.Print(redText("\nInvalid input. You may only use letters in the contact name and at least 1 letter is required.\n"))
				continue
			} else {
				break
			}
		}
	}
	if useCaseNumber {
		for {
			fmt.Print("Enter the Salesforce Case Number associated with these scripts. Press Enter to skip.\n")
			input, err = rl.Readline()
			if err != nil {
				if err.Error() == "Interrupt" {
					fmt.Println(redText("Exiting from user input."))
				} else {
					fmt.Print(redText("Error reading line:", err))
					continue
				}
				return
			}
			input = strings.TrimSpace(input)

			// Don't accept anything other than numbers and blank input.
			if input == "" {
				useCaseNumber = false
				break
			} else if _, err := strconv.Atoi(input); err == nil && input != "" {
				caseNumber, _ = strconv.Atoi(input)
				if caseNumber < 01000000 {
					fmt.Print(redText("Are you sure that's the right Case Number? It seems a bit too small.\n"))
					continue
				} else if caseNumber > 20000000 {
					fmt.Print(redText("Are you sure that's the right Case Number? It seems a bit too large.\n"))
					continue
				}
				break
			} else {
				fmt.Print(redText("Invalid entry. "))
				continue
			}
		}
	}

	for {
		fmt.Print("Enter the number of clusters you'd like to make scripts for. Entering nothing will select 1.\n")
		input, err = rl.Readline()
		if err != nil {
			if err.Error() == "Interrupt" {
				fmt.Println(redText("Exiting from user input."))
			} else {
				fmt.Print(redText("Error reading line:", err))
				continue
			}
			return
		}
		input = strings.TrimSpace(input)

		// Don't accept anything other than numbers and blank input.
		if input == "" {
			clusterCount = 1
			break
		}

		if _, err := strconv.Atoi(input); err == nil {
			clusterCount, _ = strconv.Atoi(input)
			if clusterCount < 1 {
				fmt.Print(redText("Invalid entry. You've selected zero or less clusters to create scripts for.\n"))
				continue
			}
			break
		} else {
			fmt.Print(redText("Invalid entry. Please enter an integer greater than zero.\n"))
			continue
		}
	}

	// Loop cluster creation for as many times as you specified.
	for i := 1; i <= clusterCount; i++ {
		for {
			fmt.Print("Enter cluster #", i, "'s name. Entering nothing will use \"HPC\"\n")
			clusterName, err = rl.Readline()
			if err != nil {
				if err.Error() == "Interrupt" {
					fmt.Println(redText("Exiting from user input."))
				} else {
					fmt.Print(redText("Error reading line:", err))
					continue
				}
				return
			}
			clusterName = strings.TrimSpace(clusterName)

			if clusterName == "" {
				clusterName = "HPC"
				break
			} else if lettersAndNumbersPattern.MatchString(clusterName) && clusterName != "" {
				fmt.Print(redText("Invalid input. You must include at least 1 letter or number in the cluster's name.\n"))
				continue
			} else {
				break
			}
		}

		// Map numbers to actual scheduler names.
		schedulerMap := map[int]string{
			1: "Slurm",
			2: "PBS",
			3: "LSF",
			4: "Grid Engine",
			5: "HTCondor",
			6: "AWS",
			7: "Kubernetes",
		}

		// waaaahhhh it's too difficult to just say "while".
		for {
			fmt.Print("Select the scheduler you'd like to use by entering its corresponding number. Entering nothing will select Slurm.\n")
			fmt.Print("[1 Slurm] [2 PBS] [3 LSF] [4 Grid Engine] [5 HTCondor] [6 AWS] [7 Kubernetes]\n")
			schedulerSelected, err = rl.Readline()
			if err != nil {
				if err.Error() == "Interrupt" {
					fmt.Println(redText("Exiting from user input."))
				} else {
					fmt.Print(redText("Error reading line:", err))
					continue
				}
				return
			}

			if schedulerSelected == "" {
				schedulerSelected = "Slurm"
				break
			}

			// Parse for an integer to make for some prettier code later.
			var schedulerNumberSelected int
			parsedInt, err := strconv.Atoi(schedulerSelected)
			if err != nil {
				fmt.Print(redText("\nYou did not enter a number. Enter a number to select a scheduler.\n"))
				continue
			} else {
				schedulerNumberSelected = parsedInt
			}

			if schedulerNumberSelected < 1 || schedulerNumberSelected > 7 {
				fmt.Print(redText("\nYou selected an invalid number. You must select a number between 1-7.\n"))
				schedulerSelected = schedulerMap[schedulerNumberSelected]
				continue
			} else {
				break
			}
		}

		for {
			fmt.Print("Would you like to use include the custom MPI file? (y/n) Entering nothing will not include it.\n")
			customMPIInput, err = rl.Readline()
			if err != nil {
				if err.Error() == "Interrupt" {
					fmt.Println(redText("Exiting from user input."))
				} else {
					fmt.Print(redText("Error reading line:", err))
					continue
				}
				return
			}

			strings.TrimSpace(strings.ToLower(customMPIInput))

			if customMPIInput == "" {
				customMPI = false
				break
			} else if customMPIInput == "y" || customMPIInput == "yes" {
				customMPI = true
				break
			} else if customMPIInput == "n" || customMPIInput == "no" {
				customMPI = false
				break
			} else {
				fmt.Print(redText("\nInvalid input. You must enter one of the following: \"y\" or \"n\".\n"))
				continue
			}
		}

		for {
			fmt.Print("Select the submissions types you'd like to include by entering its corresponding number. Entering nothing will select both.\n")
			fmt.Print("[1 Desktop] [2 Cluster] [3 Both]\n")
			submissionType, err = rl.Readline()
			if err != nil {
				if err.Error() == "Interrupt" {
					fmt.Println(redText("Exiting from user input."))
				} else {
					fmt.Print(redText("Error reading line:", err))
					continue
				}
				return
			}
			submissionType = strings.TrimSpace(strings.ToLower(submissionType))

			if submissionType == "" {
				submissionType = "both"
				break
			} else if submissionType == "1" || submissionType == "2" || submissionType == "3" {
				switch submissionType {
				case "1":
					submissionType = "desktop"
				case "2":
					submissionType = "cluster"
				case "3":
					submissionType = "both"
				}
				break
			} else if submissionType == "cluster" || submissionType == "desktop" || submissionType == "both" {
				break
			} else {
				fmt.Print(redText("Invalid entry. Enter a number between 1-3 to select a submission type.\n"))
				continue
			}
		}

		// # Add some code that'll ask the user if they want to include the remote submission scripts.

		for {
			fmt.Print("Enter the number of workers available on the cluster's license. Entering nothing will select 100,000.\n")
			input, err = rl.Readline()
			if err != nil {
				if err.Error() == "Interrupt" {
					fmt.Println(redText("Exiting from user input."))
				} else {
					fmt.Print(redText("Error reading line:", err))
					continue
				}
				return
			}
			input = strings.TrimSpace(input)

			if input == "" {
				numberOfWorkers = 100000
				break
				fmt.Print(numberOfWorkers) // Again, Go, shut up.
			}

			// Don't accept anything other than numbers.
			if _, err := strconv.Atoi(input); err == nil {
				numberOfWorkers, _ = strconv.Atoi(input)
				if numberOfWorkers < 1 {
					fmt.Print(redText("Invalid entry. You've selected zero or less workers.\n"))
					continue
				} else if numberOfWorkers > 100000 {
					fmt.Print(redText("Invalid entry. You've selected more than 100000 workers, which is not offered on any license.\n"))
					continue
				}
				break
			} else {
				fmt.Print(redText("Invalid entry. You've likely included a character other than a number.\n"))
				continue
			}
		}
		if submissionType == "desktop" || submissionType == "both" {
			for {
				fmt.Print("Does the client have a shared filesystem with the cluster? (y/n)\n")
				input, err = rl.Readline()
				if err != nil {
					if err.Error() == "Interrupt" {
						fmt.Println(redText("Exiting from user input."))
					} else {
						fmt.Print(redText("Error reading line:", err))
						continue
					}
					return
				}
				input = strings.TrimSpace(strings.ToLower(input))

				if input == "y" || input == "yes" {
					hasSharedFileSystem = true
					break
					fmt.Print(hasSharedFileSystem) // Again, shut up, Go. It'll be used at some point, I promise.
				} else if input == "n" || input == "no" {
					hasSharedFileSystem = false
					break
				} else {
					fmt.Print(redText("Invalid entry.\n"))
					continue
				}
			}

			for {
				fmt.Print("What is the full filepath of MATLAB on the cluster? (ex: /usr/local/MATLAB/R2023b)\n")
				clusterMatlabRoot, err = rl.Readline()
				if err != nil {
					if err.Error() == "Interrupt" {
						fmt.Println(redText("Exiting from user input."))
					} else {
						fmt.Print(redText("Error reading line:", err))
						continue
					}
					return
				}
				clusterMatlabRoot = strings.TrimSpace(clusterMatlabRoot)

				if strings.Contains(clusterMatlabRoot, "/") || strings.Contains(clusterMatlabRoot, "\\") {
					break
				} else {
					fmt.Print(redText("Invalid filepath. "))
					continue
				}
			}

			for {
				fmt.Print("What is the hostname, FQDN, or IP address used to SSH to the cluster?\n")
				clusterHostname, err = rl.Readline()
				if err != nil {
					if err.Error() == "Interrupt" {
						fmt.Println(redText("Exiting from user input."))
					} else {
						fmt.Print(redText("Error reading line:", err))
						continue
					}
					return
				}
				clusterHostname = strings.TrimSpace(clusterHostname)

				if clusterHostname == "" {
					fmt.Print(redText("Invalid entry. "))
					continue
				} else {
					break
				}
			}

			for {
				fmt.Print("Where will remote job storage location be on the cluster? Entering nothing will select /home/$USER/.matlab/generic_cluster_jobs/" + clusterName + "/$Host\n")
				remoteJobStorageLocation, err = rl.Readline()
				if err != nil {
					if err.Error() == "Interrupt" {
						fmt.Println(redText("Exiting from user input."))
					} else {
						fmt.Print(redText("Error reading line:", err))
						continue
					}
					return
				}
				remoteJobStorageLocation = strings.TrimSpace(remoteJobStorageLocation)

				if strings.Contains(remoteJobStorageLocation, "/") || strings.Contains(clusterMatlabRoot, "\\") {
					break
				} else if remoteJobStorageLocation == "" {
					remoteJobStorageLocation = "/home/$USER/.matlab/generic_cluster_jobs/" + clusterName + "/$HOST"
					break
				} else {
					fmt.Print(redText("Invalid filepath. "))
					continue
				}
			}
		}
		fmt.Print("Creating integration scripts for cluster #", i, "...\n")
		// This is where Big Things Part 1(tm) will happen.
		if useCaseNumber {
			fmt.Print("Case number: ", caseNumber, "\n")
		}
		if customMPI {
			fmt.Print("you did it. custom mpi. yipee.")
		}
		fmt.Print("Finished script creation for cluster #", i, "!\n")
	}
	fmt.Print("Submitting to your remote Git repo...\n")
	// This is where Big Things Part 2(tm) will happen (sort of.)
	fmt.Print("Finished!\n")
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

// Function to unzip integration scripts.
func unzipFile(src, dest string) error {
	reader, err := zip.OpenReader(src)
	if err != nil {
		return err
	}
	defer reader.Close()

	for _, file := range reader.File {
		path := filepath.Join(dest, file.Name)

		// Reconstruct the file path on Windows to ensure proper subdirectories are created. Don't know why other OSes don't need this.
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
