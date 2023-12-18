package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"syscall"

	"github.com/akamensky/argparse"
	"github.com/mitchellh/go-homedir"
	"golang.org/x/term"
)

func getFail(url string) bool {
	fmt.Printf("GET %s\n", url)
	response, err := http.Get(url)
	if err != nil || response.StatusCode != 200 {
		return false
	}
	return true
}

func get(url string) (*http.Response, error) {
	fmt.Printf("GET %s\n", url)
	response, err := http.Get(url)
	if err != nil || response.StatusCode != 200 {
		return nil, err
	}
	return response, nil
}

func getWithAuth(url string, accessToken string) (*http.Response, error) {
	fmt.Printf("GET %s\n", url)
	request, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Add("Authorization", fmt.Sprintf("Bearer %s", accessToken))
	response, err := http.DefaultClient.Do(request)
	if err != nil || response.StatusCode != 200 {
		return nil, err
	}
	return response, nil
}

func post(urlString string, bodyMap interface{}) (*http.Response, error) {
	fmt.Printf("POST %s\n", urlString)
	body, err := json.Marshal(bodyMap)
	if err != nil {
		return nil, err
	}
	url, err := url.Parse(urlString)
	if err != nil {
		return nil, err
	}
	bodyReader := io.NopCloser(bytes.NewReader(body))
	request := &http.Request{
		Method: "POST",
		URL:    url,
		Body:   bodyReader,
		Header: map[string][]string{
			"Content-Type": {"application/json"},
		},
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil || response.StatusCode != 200 {
		return nil, err
	}
	return response, nil
}

func makeIdentifier() string {
	// get username from environment
	lUser := os.Getenv("USER")
	lHostName := os.Getenv("HOSTNAME")
	return fmt.Sprintf("%s@%s using go-matrix", lUser, lHostName)
}

func credentials() (string, string, string, error) {
	// get url, user, password from stdin
	reader := bufio.NewReader(os.Stdin)
	fmt.Print("Server address: ")
	serverAddress, err := reader.ReadString('\n')
	if err != nil {
		return "", "", "", err
	}
	serverAddress = strings.TrimSuffix(serverAddress, "\n") // strip trailing newline
	serverAddress = fmt.Sprintf("https://%s", strings.TrimPrefix(serverAddress, "https://"))
	serverAddress = strings.TrimSuffix(serverAddress, "/") // strip trailing slash
	if !getFail(fmt.Sprintf("%s/_matrix/client/versions", serverAddress)) {
		fmt.Printf("Server %s does not appear to be a matrix server\n", serverAddress)
		os.Exit(1)
	}

	fmt.Print("Username: ")
	username, err := reader.ReadString('\n')
	if err != nil {
		return "", "", "", err
	}
	username = strings.TrimSuffix(username, "\n") // strip trailing newline

	fmt.Print("Password: ")
	password, err := term.ReadPassword(int(syscall.Stdin))
	if err != nil {
		return "", "", "", err
	}
	fmt.Println()

	return serverAddress, username, string(password), nil
}

func login() {
	serverAddress, username, password, err := credentials()
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	fmt.Printf("username: %s\nPassword: %s\nUrl: %s\n", username, password, serverAddress)
	jsonBodyMap := interface{}(map[string]interface{}{
		"type": "m.login.password",
		"identifier": map[string]interface{}{
			"type": "m.id.user",
			"user": username,
		},
		"password":                    password,
		"initial_device_display_name": makeIdentifier(),
	})
	response, err := post(fmt.Sprintf("%s/_matrix/client/r0/login", serverAddress), jsonBodyMap)
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	// convert body to map
	var bodyMap map[string]interface{}
	err = json.Unmarshal(body, &bodyMap)
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	accessToken := bodyMap["access_token"].(string)
	userId := bodyMap["user_id"].(string)
	homeServer := bodyMap["home_server"].(string)

	// save credentials to file
	homeDirPath, err := homedir.Dir()
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	path := fmt.Sprintf("%s/.config/gatrix", homeDirPath)
	credentialsFile, err := os.Create(path)
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	defer credentialsFile.Close()
	jsonString, err := json.Marshal(map[string]string{
		"access_token": accessToken,
		"user_id":      userId,
		"home_server":  homeServer,
	})
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	_, err = credentialsFile.Write(jsonString)
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	// change permissions to 600
	err = os.Chmod(path, 0600)
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}

func listRooms(credentials map[string]string) error {
	println("getting room data...")
	url := fmt.Sprintf("https://%s/_matrix/client/r0/sync", credentials["home_server"])
	response, err := getWithAuth(url, credentials["access_token"])
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	if response.StatusCode != 200 {
		fmt.Printf("response status code: %d\n", response.StatusCode)
		os.Exit(1)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	if body == nil {
		fmt.Println("body is nil")
		os.Exit(1)
	}
	var bodyMap map[string]interface{}
	err = json.Unmarshal(body, &bodyMap)
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	rooms := bodyMap["rooms"].(map[string]interface{})
	if rooms["join"] != nil {
		println("joined rooms:")
		join := rooms["join"].(map[string]interface{})
		for key, value := range join {
			room := value.(map[string]interface{})
			state := room["state"].(map[string]interface{})
			events := state["events"].([]interface{})
			name := ""
			for _, event := range events {
				eventMap := event.(map[string]interface{})
				if eventMap["type"].(string) == "m.room.name" {
					name = eventMap["content"].(map[string]interface{})["name"].(string)
				}
			}
			if name == "" {
				name = "<Unnamed room>"
			}
			fmt.Printf("%s: %s\n", key, name)
		}
		println()
	} else {
		println("no joined rooms")
	}
	if rooms["invite"] != nil {
		invite := rooms["invite"].(map[string]interface{})
		println("invited rooms:")
		for key, value := range invite {
			room := value.(map[string]interface{})
			state := room["state"].(map[string]interface{})
			events := state["events"].([]interface{})
			name := ""
			for _, event := range events {
				eventMap := event.(map[string]interface{})
				if eventMap["type"].(string) == "m.room.name" {
					name = eventMap["content"].(map[string]interface{})["name"].(string)
				}
			}
			if name == "" {
				name = "<Unnamed room>"
			}
			fmt.Printf("%s: %s\n", key, name)
		}
	} else {
		println("no invited rooms")
	}
	return nil
}

func readConfigFile(path string) (map[string]string, error) {
	credentials, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	credentialsMap := make(map[string]string)
	err = json.Unmarshal(credentials, &credentialsMap)
	if err != nil {
		return nil, err
	}
	return credentialsMap, nil
}

func main() {
	homeDir, err := homedir.Dir()
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	credentialsPath := fmt.Sprintf("%s/.config/gatrix", homeDir)
	if _, err := os.Stat(credentialsPath); os.IsNotExist(err) {
		fmt.Println("credentials cannot be found, please run login with --login")
		return
	}
	credentialsMap, err := readConfigFile(credentialsPath)
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	parser := argparse.NewParser("gatrix", "A command line matrix client")
	bLogin := parser.Flag("l", "login", &argparse.Options{Required: false, Help: "login to matrix server"})
	list := parser.Flag("", "list-rooms", &argparse.Options{Required: false, Help: "list rooms"})
	err = parser.Parse(os.Args)
	if err != nil {
		fmt.Print(parser.Usage(err))
	}
	if *bLogin {
		login()
	} else if *list {
		listRooms(credentialsMap)
	}
}
