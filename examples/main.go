package main

import (
	"fmt"
	"github.com/snowirbis/twinkly"
)

func main() {
	tw := twinkly.New("http://192.168.1.80")

	tw.SetColor(twinkly.Color{Red: 255, Green: 255, Blue: 255})

	brightness := 100

	tw.SetBrightness(brightness)

	movies, _ := tw.GetMovies()

	fmt.Println("Stored movies:")

	framesUsed := 0

	for _, value := range movies.Movies {
		fmt.Printf("%d - %s - %d\n", value.ID, value.Name, value.FramesNumber)
		framesUsed += value.FramesNumber
	}
	fmt.Printf("Used %d frames of %d available\n", framesUsed, movies.AvailableFrames)

	// tm.SetMovie(3)
	// tm.SetBrightness(50)

}
