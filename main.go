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
	"github.com/robert-nix/ansihtml"
	logging "github.com/saizo80/go-logging"
	"golang.org/x/term"
)

var (
	log *logging.Logger
)

const (
	dummyAuth = "X-Dummy: 1"
)

func main() {
	log = logging.New(logging.INFO)
	homeDir, err := homedir.Dir()
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	credentialsPath := fmt.Sprintf("%s/.config/gatrix", homeDir)

	parser := argparse.NewParser("gatrix", "A command line matrix client")
	bLogin := parser.Flag("l", "login", &argparse.Options{Required: false, Help: "login to matrix server"})
	list := parser.Flag("", "list-rooms", &argparse.Options{Required: false, Help: "list rooms"})
	debug := parser.Flag("d", "debug", &argparse.Options{Required: false, Help: "enable debug logging"})
	join := parser.Flag("j", "join", &argparse.Options{Required: false, Help: "join room (requires --room)"})
	leave := parser.Flag("", "leave", &argparse.Options{Required: false, Help: "leave room (requires --room)"})
	roomId := parser.String("r", "room", &argparse.Options{Required: false, Help: "room id (i.e. !abc123:matrix.org)"})
	bSend := parser.Flag("s", "send", &argparse.Options{Required: false, Help: "send message (requires --room and --message or piped input)"})
	message := parser.String("m", "message", &argparse.Options{Required: false, Help: "message to send (requires --send)"})
	ansi := parser.Flag("", "ansi", &argparse.Options{Required: false, Help: "enable ansi escape codes"})
	err = parser.Parse(os.Args)
	if err != nil {
		fmt.Print(parser.Usage(err))
	}
	if *debug {
		log.SetLevel(logging.DEBUG)
	}
	if *bLogin {
		login()
	}
	if _, err := os.Stat(credentialsPath); os.IsNotExist(err) {
		fmt.Println("credentials cannot be found, please run login with --login")
		return
	}
	credentialsMap, err := readConfigFile(credentialsPath)
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	if *list {
		listRooms(credentialsMap)
	} else if *join {
		if *roomId == "" {
			fmt.Println("room id is required")
			err := listRooms(credentialsMap)
			if err != nil {
				fmt.Println(err)
				os.Exit(1)
			}
			os.Exit(0)
		}
		joinRoom(credentialsMap, *roomId)
	} else if *leave {
		if *roomId == "" {
			fmt.Println("room id is required")
			os.Exit(0)
		}
		leaveRoom(credentialsMap, *roomId)
	} else if *bSend {
		if *roomId == "" {
			fmt.Println("room id is required")
			err := listRooms(credentialsMap)
			if err != nil {
				fmt.Println(err)
				os.Exit(1)
			}
			os.Exit(0)
		}
		if *message == "" {
			*message, err = getPipedInput()
			if err != nil {
				fmt.Println(err)
				os.Exit(1)
			}
		}
		log.Debug("sending message: %s", *message)
		println("sending message...")
		err := sendMessage(credentialsMap, *roomId, *message, *ansi)
		if err != nil {
			fmt.Println(err)
			os.Exit(1)
		}
	}
}

func get(url string, accessToken string) (*http.Response, error) {
	if accessToken == "" {
		accessToken = dummyAuth
	}
	log.Debug("GET %s", url)
	request, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Add("Authorization", fmt.Sprintf("Bearer %s", accessToken))
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return nil, err
	}
	if response.StatusCode != 200 {
		println("an error occurred while communicating with the server")
		printMatrixError(response)
		os.Exit(2)
	}
	return response, nil
}

func post(urlString string, bodyMap interface{}, accessToken string) (*http.Response, error) {
	if accessToken == "" {
		accessToken = dummyAuth
	}
	log.Debug("POST %s", urlString)
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
			"Content-Type":  {"application/json"},
			"Authorization": {fmt.Sprintf("Bearer %s", accessToken)},
		},
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return nil, err
	}
	if response.StatusCode != 200 {
		println("an error occurred while communicating with the server")
		printMatrixError(response)
		os.Exit(2)
	}
	return response, nil
}

func parseAnsi(message string) string {
	html := string(ansihtml.ConvertToHTML([]byte(message)))
	return fmt.Sprintf("<pre>%s</pre>", html)
}

func sendMessage(credentialsMap map[string]string, roomId string, message string, ansi bool) error {
	url := fmt.Sprintf("https://%s/_matrix/client/r0/rooms/%s/send/m.room.message", credentialsMap["home_server"], roomId)
	message = strings.ReplaceAll(message, "\\n", "\n")
	message = strings.ReplaceAll(message, "\\t", "\t")
	// replace quotes with unicode quotes
	// message = strings.ReplaceAll(message, "\"", "”")
	jsonBodyMap := map[string]interface{}{
		"msgtype": "m.text",
		"body":    message,
	}
	if ansi {
		jsonBodyMap["format"] = "org.matrix.custom.html"
		jsonBodyMap["formatted_body"] = parseAnsi(message)
	}
	for key, value := range jsonBodyMap {
		log.Debug("%s: %s", key, value)
	}
	_, err := post(url, jsonBodyMap, credentialsMap["access_token"])
	if err != nil {
		return err
	}
	return nil
}

func printMatrixError(response *http.Response) {
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
	errcode := bodyMap["errcode"].(string)
	errorMessage := bodyMap["error"].(string)
	fmt.Printf("error: %s (%s)\n", errorMessage, errcode)
}

func makeIdentifier() string {
	// get username from environment
	lUser := os.Getenv("USER")
	lHostName, err := os.Hostname()
	if err != nil {
		lHostName = ""
	}
	return fmt.Sprintf("%s@%s using gatrix", lUser, lHostName)
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
	response, err := get(fmt.Sprintf("%s/_matrix/client/versions", serverAddress), "")
	if err != nil {
		return "", "", "", err
	}
	if response.StatusCode != 200 {
		fmt.Printf("response status code: %d\n", response.StatusCode)
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
	jsonBodyMap := interface{}(map[string]interface{}{
		"type": "m.login.password",
		"identifier": map[string]interface{}{
			"type": "m.id.user",
			"user": username,
		},
		"password":                    password,
		"initial_device_display_name": makeIdentifier(),
	})
	response, err := post(fmt.Sprintf("%s/_matrix/client/r0/login", serverAddress), jsonBodyMap, "")
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
	response, err := get(url, credentials["access_token"])
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
				timeline := room["timeline"].(map[string]interface{})
				events := timeline["events"].([]interface{})
				for _, event := range events {
					eventMap := event.(map[string]interface{})
					if eventMap["type"].(string) == "m.room.name" {
						name = eventMap["content"].(map[string]interface{})["name"].(string)
					}
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
			state := room["invite_state"].(map[string]interface{})
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

func joinRoom(credentials map[string]string, roomId string) error {
	url := fmt.Sprintf("https://%s/_matrix/client/r0/join/%s", credentials["home_server"], roomId)
	_, err := post(url, map[string]interface{}{}, credentials["access_token"])
	if err != nil {
		return err
	}
	return nil
}

func leaveRoom(credentials map[string]string, roomId string) error {
	url := fmt.Sprintf("https://%s/_matrix/client/r0/rooms/%s/leave", credentials["home_server"], roomId)
	_, err := post(url, map[string]interface{}{}, credentials["access_token"])
	if err != nil {
		return err
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

func getPipedInput() (string, error) {
	reader := bufio.NewReader(os.Stdin)
	var buffer bytes.Buffer
	for {
		input, err := reader.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				break
			}
			return "", err
		}
		buffer.WriteString(input)
	}
	// trim trailing newline
	returnString := buffer.String()
	log.Debug("returnString: %s", returnString)
	returnString = strings.TrimSuffix(returnString, "\n")
	return returnString, nil
}
