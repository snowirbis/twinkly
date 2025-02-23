package twinkly

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"math/rand"
	"net/http"
	"sync"
	"time"
)

type LoginResponse struct {
	AuthenticationToken string `json:"authentication_token"`
	ChallengeResponse   string `json:"challenge-response"`
	TokenExpiresIn      int    `json:"authentication_token_expires_in"`
}

type Color struct {
	Red   int `json:"red"`
	Green int `json:"green"`
	Blue  int `json:"blue"`
}

type Movie struct {
	ID             int    `json:"id"`
	Name           string `json:"name"`
	UniqueID       string `json:"unique_id"`
	DescriptorType string `json:"descriptor_type"`
	LedsPerFrame   int    `json:"leds_per_frame"`
	FramesNumber   int    `json:"frames_number"`
	FPS            int    `json:"fps"`
}

type MovieResponse struct {
	Movies          []Movie `json:"movies"`
	AvailableFrames int     `json:"available_frames"`
	MaxCapacity     int     `json:"max_capacity"`
	Max             int     `json:"max"`
	Code            int     `json:"code"`
}

type TwinklyManager struct {
	mu     sync.Mutex
	apiUrl string
	token  string
	ticker *time.Ticker
}

func New(url string) *TwinklyManager {
	tm := &TwinklyManager{
		apiUrl: url,
	}
	tm.Authenticate()
	return tm
}

func (tm *TwinklyManager) Authenticate() error {
	loginResponse, err := tm.TWlogin(tm.generateChallenge(32))
	if err != nil {
		return err
	}

	err = tm.TWverify(loginResponse.ChallengeResponse, loginResponse.AuthenticationToken)
	if err != nil {
		return err
	}

	tm.SetToken(loginResponse.AuthenticationToken)
	tm.startTokenRefresh()

	return nil
}

func (tm *TwinklyManager) SetToken(token string) {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	tm.token = token
}

func (tm *TwinklyManager) GetToken() (string, error) {

	if tm.token == "" {
		if err := tm.Authenticate(); err != nil {
			return "", err
		}
	}

	return tm.token, nil
}

func (tm *TwinklyManager) startTokenRefresh() {
	if tm.ticker != nil {
		tm.ticker.Stop() // Зупиняємо попередній тікер, якщо він існує
	}

	tm.ticker = time.NewTicker(14000 * time.Second)

	go func() {
		for range tm.ticker.C {
			_ = tm.Authenticate() // Оновлення токена
		}
	}()
}

func (tm *TwinklyManager) SetColor(color Color) error {

	token, err := tm.GetToken()
	if err != nil {
		return err
	}
	return tm.TWsetColor(color, token)
}

func (tm *TwinklyManager) SetBrightness(brightness int) error {
	token, err := tm.GetToken()
	if err != nil {
		return err
	}
	return tm.TWsetBrightness(brightness, token)
}

func (tm *TwinklyManager) SetMovie(id int) error {
	token, err := tm.GetToken()
	if err != nil {
		return err
	}
	return tm.TWsetMovie(id, token)
}

func (tm *TwinklyManager) GetMovies() (MovieResponse, error) {
	token, err := tm.GetToken()
	if err != nil {
		return MovieResponse{}, err
	}

	movies, err := tm.TWgetMovies(token)

	return movies, err
}

func (tm *TwinklyManager) TWlogin(challenge string) (*LoginResponse, error) {
	data := map[string]string{"challenge": challenge}
	response, err := tm.TWrequest("POST", "/xled/v1/login", data)
	if err != nil {
		return nil, err
	}

	var loginResponse LoginResponse
	if err := json.Unmarshal(response, &loginResponse); err != nil {
		return nil, err
	}

	return &loginResponse, nil
}

func (tm *TwinklyManager) TWverify(challengeResponse, token string) error {
	data := map[string]string{"challenge-response": challengeResponse}
	_, err := tm.TWrequest("POST", "/xled/v1/verify", data, token)
	return err
}

func (tm *TwinklyManager) TWsetColor(color Color, token string) error {
	mode := map[string]string{"mode": "color"}
	_, err := tm.TWrequest("POST", "/xled/v1/led/mode", mode, token)
	_, err = tm.TWrequest("POST", "/xled/v1/led/color", color, token)
	return err
}

func (tm *TwinklyManager) TWsetBrightness(brightness int, token string) error {
	data := map[string]interface{}{"mode": "enabled", "type": "A", "value": brightness}
	_, err := tm.TWrequest("POST", "/xled/v1/led/out/brightness", data, token)
	return err
}

func (tm *TwinklyManager) TWsetMovie(id int, token string) error {
	data := map[string]int{"id": id - 1}
	mode := map[string]string{"mode": "movie"}
	_, err := tm.TWrequest("POST", "/xled/v1/led/mode", mode, token)
	if err != nil {
		return err
	}
	_, err = tm.TWrequest("POST", "/xled/v1/movies/current", data, token)
	return err
}

func (tm *TwinklyManager) TWgetMovies(token string) (MovieResponse, error) {
	data, _ := tm.TWrequest("GET", "/xled/v1/movies", "", token)

	var movies MovieResponse
	json.Unmarshal([]byte(data), &movies)

	return movies, nil
}

func (tm *TwinklyManager) TWrequest(method, endpoint string, data interface{}, token ...string) ([]byte, error) {
	jsonData, _ := json.Marshal(data)
	contentLength := len(jsonData)

	req, err := http.NewRequest(method, tm.apiUrl+endpoint, bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, fmt.Errorf("TWrequest.NewRequest error - %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Content-Length", fmt.Sprintf("%d", contentLength))
	if len(token) > 0 {
		req.Header.Set("X-Auth-Token", token[0])
	}

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("TWrequest.client.Do error - %w", err)
	}
	defer resp.Body.Close()

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("TWrequest.ioutil.ReadAll error - %w", err)
	}

	return body, nil
}

func (tm *TwinklyManager) generateChallenge(length int) string {
	characters := "0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"
	rand.Seed(time.Now().UnixNano())
	randomString := make([]byte, length)
	for i := range randomString {
		randomString[i] = characters[rand.Intn(len(characters))]
	}
	return base64.StdEncoding.EncodeToString(randomString)
}
