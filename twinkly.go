package twinkly

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	Version = "0.0.4"
)

type Gestalt struct {
	FWFamily          string  `json:"fw_family"`           // F/G only
	ProductName       string  `json:"product_name"`        // D/F/G
	ProductVersion    string  `json:"product_version"`     // D only (numeric string)
	HardwareVersion   string  `json:"hardware_version"`    // D/F/G (numeric string)
	BytesPerLed       int     `json:"bytes_per_led"`       // D/F
	FlashSize         int     `json:"flash_size"`          // D/F/G
	LedType           int     `json:"led_type"`            // D/F/G
	LedVersion        string  `json:"led_version"`         // D only
	ProductCode       string  `json:"product_code"`        // D/F/G
	DeviceName        string  `json:"device_name"`         // D/F/G
	RSSI              int     `json:"rssi"`                // D only (>=2.1.0)
	Uptime            string  `json:"uptime"`              // D: seconds string, F/G: milliseconds string
	HWID              string  `json:"hw_id"`               // D/F/G
	MAC               string  `json:"mac"`                 // D/F/G
	UUID              string  `json:"uuid"`                // D/F/G
	MaxSupportedLed   int     `json:"max_supported_led"`   // D/F/G
	BaseLedsNumber    int     `json:"base_leds_number"`    // D only
	NumberOfLed       int     `json:"number_of_led"`       // D/F/G
	LedProfile        string  `json:"led_profile"`         // D/F/G
	FrameRate         float64 `json:"frame_rate"`          // D:int but ok as float64, F/G: float
	MeasuredFrameRate float64 `json:"measured_frame_rate"` // F/G only (>=2.5.6)
	MovieCapacity     int     `json:"movie_capacity"`      // D/F/G
	Copyright         string  `json:"copyright"`           // D/F/G
	WireType          int     `json:"wire_type"`           // G only

	Code int `json:"code"` // app return code
}

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

type Brightness struct {
	Value int `json:"value"`
}

type Mode struct {
	Mode string `json:"mode"`
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

type PingResponse struct {
	URL      string `json:"url"`
	Reply    bool   `json:"reply"`
	AuthOK   bool   `json:"auth_ok"`
	TokenSet bool   `json:"token_set"`
	HTTPCode int    `json:"http_code,omitempty"`
	ErrStage string `json:"err_stage,omitempty"`
	Err      string `json:"err,omitempty"`
}

type TwinklyManager struct {
	reqMu sync.Mutex
	tokMu sync.Mutex

	apiUrl string
	token  string

	ticker      *time.Ticker
	refreshStop chan struct{}

	httpClient *http.Client
}

func (tm *TwinklyManager) GetVersion() string {
	return Version
}

func New(url string) *TwinklyManager {
	tm := &TwinklyManager{
		apiUrl: strings.TrimRight(url, "/"),
		httpClient: &http.Client{
			Timeout: 5 * time.Second,
		},
	}
	_ = tm.Authenticate()
	return tm
}

func (tm *TwinklyManager) Ping() (*PingResponse, error) {
	res := &PingResponse{
		URL: tm.apiUrl,
	}

	// 1) lightweight reply check: gestalt without token (some firmwares may still require token,
	// but if it responds with 401/404 we still know device is reachable)
	req, err := http.NewRequest("GET", tm.apiUrl+"/xled/v1/gestalt", nil)
	if err != nil {
		res.ErrStage = "new_request"
		res.Err = err.Error()
		return res, err
	}
	req.Header.Set("Accept", "application/json")

	resp, err := tm.httpClient.Do(req)
	if err != nil {
		res.ErrStage = "http_do"
		res.Err = err.Error()
		return res, err
	}
	res.HTTPCode = resp.StatusCode
	_ = resp.Body.Close()
	res.Reply = true

	// 2) auth check
	if err := tm.Authenticate(); err != nil {
		res.ErrStage = "authenticate"
		res.Err = err.Error()
		// Reply already true, but auth failed
		return res, err
	}

	tok, err := tm.GetToken()
	if err != nil {
		res.ErrStage = "get_token"
		res.Err = err.Error()
		return res, err
	}

	res.TokenSet = tok != ""
	res.AuthOK = res.TokenSet

	return res, nil
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
	tm.tokMu.Lock()
	tm.token = token
	tm.tokMu.Unlock()
}

func (tm *TwinklyManager) GetToken() (string, error) {
	tm.tokMu.Lock()
	tok := tm.token
	tm.tokMu.Unlock()

	if tok == "" {
		if err := tm.Authenticate(); err != nil {
			return "", err
		}
		tm.tokMu.Lock()
		tok = tm.token
		tm.tokMu.Unlock()
	}

	return tok, nil
}

func (tm *TwinklyManager) startTokenRefresh() {
	tm.tokMu.Lock()

	// stop previous goroutine + ticker
	if tm.refreshStop != nil {
		close(tm.refreshStop)
		tm.refreshStop = nil
	}
	if tm.ticker != nil {
		tm.ticker.Stop()
		tm.ticker = nil
	}

	// start new
	tm.refreshStop = make(chan struct{})
	tm.ticker = time.NewTicker(14000 * time.Second)

	stop := tm.refreshStop
	t := tm.ticker

	tm.tokMu.Unlock()

	go func(t *time.Ticker, stop <-chan struct{}) {
		for {
			select {
			case <-t.C:
				_ = tm.Authenticate()
			case <-stop:
				return
			}
		}
	}(t, stop)
}

func (tm *TwinklyManager) GetInfo() (Gestalt, error) {

	token, err := tm.GetToken()
	if err != nil {
		return Gestalt{}, err
	}
	return tm.TWgetGestalt(token)
}

func (tm *TwinklyManager) GetColor() (Color, error) {

	token, err := tm.GetToken()
	if err != nil {
		return Color{}, err
	}
	return tm.TWgetColor(token)
}

func (tm *TwinklyManager) GetBrightness() (int, error) {
	token, err := tm.GetToken()
	if err != nil {
		return 0, err
	}
	return tm.TWgetBrightness(token)
}

func (tm *TwinklyManager) GetMode() (string, error) {
	token, err := tm.GetToken()
	if err != nil {
		return "", err
	}

	mode, err := tm.TWgetMode(token)

	return mode, err
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

func (tm *TwinklyManager) TWgetGestalt(token string) (Gestalt, error) {
	body, err := tm.TWrequest("GET", "/xled/v1/gestalt", "", token)
	if err != nil {
		return Gestalt{}, err
	}

	var g Gestalt
	if err := json.Unmarshal(body, &g); err != nil {
		return Gestalt{}, err
	}

	return g, nil
}

func (tm *TwinklyManager) TWsetColor(color Color, token string) error {
	mode := map[string]string{"mode": "color"}
	_, err := tm.TWrequest("POST", "/xled/v1/led/mode", mode, token)
	_, err = tm.TWrequest("POST", "/xled/v1/led/color", color, token)
	return err
}

func (tm *TwinklyManager) TWgetColor(token string) (Color, error) {

	body, err := tm.TWrequest("GET", "/xled/v1/led/color", "", token)

	if err != nil {
		return Color{}, err
	}

	var resp Color

	if err := json.Unmarshal(body, &resp); err != nil {
		return Color{}, err
	}

	return Color{
		Red:   resp.Red,
		Green: resp.Green,
		Blue:  resp.Blue,
	}, nil
}

func (tm *TwinklyManager) TWsetBrightness(brightness int, token string) error {
	data := map[string]interface{}{"mode": "enabled", "type": "A", "value": brightness}
	_, err := tm.TWrequest("POST", "/xled/v1/led/out/brightness", data, token)
	return err
}

func (tm *TwinklyManager) TWgetBrightness(token string) (int, error) {
	body, err := tm.TWrequest("GET", "/xled/v1/led/out/brightness", "", token)
	if err != nil {
		return 0, err
	}

	var resp Brightness
	if err := json.Unmarshal(body, &resp); err != nil {
		return 0, err
	}

	return resp.Value, nil
}

func (tm *TwinklyManager) TWgetMode(token string) (string, error) {
	body, err := tm.TWrequest("GET", "/xled/v1/led/mode", "", token)
	if err != nil {
		return "", err
	}

	var resp Mode
	if err := json.Unmarshal(body, &resp); err != nil {
		return "", err
	}

	return resp.Mode, nil
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

	data, err := tm.TWrequest("GET", "/xled/v1/movies", "", token)

	if err != nil {
		return MovieResponse{}, err
	}

	var movies MovieResponse

	if err := json.Unmarshal(data, &movies); err != nil {
		return MovieResponse{}, err
	}

	return movies, nil
}

func (tm *TwinklyManager) TWrequest(method, endpoint string, data any, token ...string) ([]byte, error) {

	tm.reqMu.Lock()
	defer tm.reqMu.Unlock()

	var bodyReader io.Reader

	if data != nil {
		jsonData, err := json.Marshal(data)
		if err != nil {
			return nil, fmt.Errorf("TWrequest.marshal error: %w", err)
		}
		bodyReader = bytes.NewReader(jsonData)
	}

	req, err := http.NewRequest(method, tm.apiUrl+endpoint, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("TWrequest.NewRequest error: %w", err)
	}

	req.Header.Set("Accept", "application/json")
	if data != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	if len(token) > 0 && token[0] != "" {
		req.Header.Set("X-Auth-Token", token[0])
	}

	resp, err := tm.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("TWrequest.Do error: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("TWrequest.ReadAll error: %w", err)
	}

	trim := bytes.TrimSpace(raw)

	// HTTP non-2xx -> return readable error
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		cut := trim
		if len(cut) > 200 {
			cut = cut[:200]
		}
		return nil, fmt.Errorf("twinkly http %d %s: %q", resp.StatusCode, resp.Status, string(cut))
	}

	// Twinkly returns plain-text instead of JSON if error occurs
	if len(trim) > 0 && trim[0] != '{' && trim[0] != '[' {
		cut := trim
		if len(cut) > 200 {
			cut = cut[:200]
		}
		return nil, fmt.Errorf("twinkly non-json response (ct=%q): %q", resp.Header.Get("Content-Type"), string(cut))
	}

	return raw, nil
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
