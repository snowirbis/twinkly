# Twinkly

A simple Go package to manage Twinkly LED devices.

## Features
- Adjust brightness
- Set LED colors
- Select and play effects (movies)
- Automatic authentication with Twinkly API
- Token refresh every 14000 seconds

## Usage
### Initialize the manager
```go
package main

import (
        "fmt"
        "time"
        "github.com/snowirbis/twinkly"
)

func main() {
        tm := twinkly.New("http://192.168.1.80")

        fmt.Println("Twinkly manager initialized.")

        // turn all leds white
        tw.SetColor(twinkly.Color{Red: 255, Green: 255, Blue: 255})

        // with 100% brightness
        tw.SetBrightness(100)

}
```
