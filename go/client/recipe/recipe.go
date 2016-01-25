// Copyright 2016 Google, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package recipe

// The recipe package provides a way to configure worker threads in a load
// testing program. The module parses the --recipes flags (string list, so
// the flag can be specified multiple times); each --recipes string is
// supposed to be of the form: "5x100+fun(2)". In this string:
//
// 5 is the number of workers to start for this recipe
// 100 is the base QPS of each worker
// fun(2) is a function that gets executed every --recipe_interval (duration)
// to change the QPS.
//
// Apart for --recipe_interval there is also --recipe_reset: Every
// --recipe_reset (duration) the QPS of a worker is reset to the base QPS.
//
// Currently supported functions are:
// - constant_increase(x): QPS += x
// - random_change(x): QPS = base QPS + x * a random number between -1 and 1.
// - sin(x): QPS = a sine wave between 0 and x
// - inc_sin(x): QPS = a sine wave between 0 and x * the number of resets
//
// inc_sin is especially handy. If you run a load test for four hours, with
// a reset time of an hour, you will get a sine wave with a larger amplitude
// every hour. This can be used to find nice correlations between QPS, latency
// and error rates.
//
// The user of this package needs to call ParseRecipes(). This function
// returns a slice of pointers to worker state objects. The caller is
// responsible for starting each worker goroutine. In the main loop of the
// worker it needs to call WorkerState.IntervalExpired in a loop. This
// returns a boolean to indicate that either a reset interval of a recipe
// interval has expired. The WorkerState.CurrentQPS member indicates how
// much QPS the worker is supposed to do in this interval.
//
// Example:
//
// func worker(w *recipe.WorkerState) {
//   for {
//     if w.IntervalExpired() {
//       // w.CurrentQPS contains the new QPS that you should send
//     }
//     ...
//   }
// }
//
// func main() {
//   if workers, err := recipe.ParseRecipes(); err != nil {
//      log.Exitf("BOOM: %v", err)
//   }
//
//   for idx, w := range workers {
//     go worker(idx, w, reportingC)
//   }
// }

import (
	"errors"
	"fmt"
	"math"
	"math/rand"
	"regexp"
	"strconv"
	"strings"
	"time"

	"flag"

	log "github.com/golang/glog"
)

var (
	recipes        = flag.String("recipes", "", "Comma separated list of recipes to execute. Each recipe is of the form 10x100+fun(2)")
	recipeReset    = flag.Duration("recipe_reset", 30*time.Minute, "Duration after which to reset a worker's QPS to the base QPS")
	recipeInterval = flag.Duration("recipe_interval", 1*time.Minute, "Interval for executing the recipe function")
	startingTime   = time.Now()
)

// Recipe encapsulates the information from a single recipe string.
// Note: Recipe objects are r/o once they are created. That allows
// multiple workers to hold a pointer to the same recipe.
type Recipe struct {
	name    string
	baseQPS float64
	arg     []float64
	fun     QPSChangeFunc
}

// WorkerState encapsulates the recipe state for each loadtest worker.
type WorkerState struct {
	OldQPS         float64   // the QPS we did last interval
	CurrentQPS     float64   // the QPS we need to do this interval
	Recipe         *Recipe   // the recipe this worker adheres to
	lastResetTime  time.Time // the last time the recipe was reset to its initial state
	lastRecipeTime time.Time // the last time the recipe function ran
	resetCount     int       // how many resets we already did
}

// QPSChangeFunc is a helper type that describes the recipe function that runs
// every recipe interval.
type QPSChangeFunc func(w *WorkerState)

// parseFloat parses a floating point number from a string. It logs a fatal
// error if the string cannot be converted to a valid floating point number.
func parseFloat(s string) float64 {
	f, err := strconv.ParseFloat(s, 64)

	if err != nil {
		log.Exitf("Cannot convert %v to float64: %v", s, err)
	}

	return f

}

// parseListOfFloats parses a string containing a comma separated list of
// floating point numbers into a slice of floats. If any of the components
// cannot be converted to a floating point number it logs a fatal error.
func parseListOfFloats(s string) []float64 {
	a := strings.Split(s, ",")
	result := make([]float64, len(a))

	for i, n := range a {
		result[i] = parseFloat(n)
	}

	return result
}

// parseInt parses an integer number from a string. It logs a fatal
// error if the string cannot be converted to a valid integer.
func parseInt(s string) int {
	i, err := strconv.ParseInt(s, 10, 32)

	if err != nil {
		log.Exitf("Cannot convert %v to int: %v", s, err)
	}

	return int(i)
}

// checkArg checks whether the correct number of arguments were passed to
// a recipe function (in the --recipes flag).
func checkArg(r *Recipe, expect int) {
	if len(r.arg) != expect {
		log.Exitf("%v expects %v argument(s), got %v: %v", r.name, expect, len(r.arg), r.arg)
	}
}

// parseQPSChangeFunc augments a recipe with the pointer to a Go function
// that actually implements the recipe function. If the function specified
// in --recipes is unknown or an incorrect number of arguments were passed,
// it returns false, otherwise not false.
func parseQPSChangeFunc(r *Recipe) bool {
	switch r.name {
	case "constant_increase":
		checkArg(r, 1)
		r.fun = func(w *WorkerState) {
			w.CurrentQPS += w.Recipe.arg[0]
		}
		return true

	case "random_change":
		checkArg(r, 1)
		r.fun = func(w *WorkerState) {
			w.CurrentQPS = w.Recipe.baseQPS + w.Recipe.arg[0]*(1.0-2.0*rand.Float64())
		}
		return true

	case "sin":
		checkArg(r, 1)
		r.fun = func(w *WorkerState) {
			t := math.Mod(time.Now().Sub(startingTime).Seconds(), recipeReset.Seconds())
			w.CurrentQPS = w.Recipe.arg[0] * math.Sin(t/recipeReset.Seconds()*math.Pi)
		}
		return true

	case "inc_sin":
		checkArg(r, 1)
		r.fun = func(w *WorkerState) {
			t := math.Mod(time.Now().Sub(startingTime).Seconds(), recipeReset.Seconds())
			w.CurrentQPS = float64(w.resetCount) * w.Recipe.arg[0] * math.Sin(t/recipeReset.Seconds()*math.Pi)
		}
		return true
	}

	return false
}

// ParseRecipes parses the --recipes flag and returns a slice of pointers to
// a WorkerState, one for each worker that you need to start. Note: Because Recipe
// objects are r/o, multiple workers might (probably will) end up with
// pointers to the same recipe in their worker states.
func ParseRecipes() ([]*WorkerState, error) {
	if *recipes == "" {
		return nil, errors.New("Empty --recipes flag")
	}
	recipesSlice := strings.Split(*recipes, ",")

	re := regexp.MustCompile("(\\d+)x(\\d+)\\+(\\w+)\\((\\d+(\\.\\d+)?(,\\d+(\\.\\d+))*)\\)")

	// We don't know how many workers there will be, so we have no choice
	// but to make an empty slice and append to it, which is inefficient,
	// but needs to be done only during startup.
	var result []*WorkerState

	for _, r := range recipesSlice {
		log.Infof("Parsing recipe: %v", r)
		s := re.FindStringSubmatch(r)

		if s == nil {
			return nil, fmt.Errorf("Cannot parse recipe %v", r)
		}

		n := parseInt(s[1])
		r := &Recipe{
			name:    s[3],
			baseQPS: parseFloat(s[2]),
			arg:     parseListOfFloats(s[4]),
		}

		if !parseQPSChangeFunc(r) {
			return nil, fmt.Errorf("Cannot parse the function in recipe %v", r)
		}

		// Adds an entry for each worker with this recipe.
		for i := 0; i < n; i++ {
			result = append(result, &WorkerState{
				Recipe:     r,
				CurrentQPS: r.baseQPS,
			})
		}
	}

	return result, nil
}

// IntervalExpired checks if the recipe's interval or reset interval
// expired. If it has then the function returns true and it resets the
// worker state to the right values for the next interval: CurrentQPS will
// contain the QPS for the next interval, and OldQPS will contain the QPS
// of the interval that just finished. If no interval expired then the
// functional returns false.
func (w *WorkerState) IntervalExpired() bool {
	now := time.Now()
	resetExpired := w.lastResetTime.Add(*recipeReset).Before(now)
	recipeExpired := w.lastRecipeTime.Add(*recipeInterval).Before(now)

	if resetExpired {
		w.lastRecipeTime = now
		w.lastResetTime = now
		w.resetCount++
		w.OldQPS = w.CurrentQPS
		w.CurrentQPS = w.Recipe.baseQPS
	} else if recipeExpired {
		w.lastRecipeTime = now
		w.OldQPS = w.CurrentQPS
		w.Recipe.fun(w)
	}

	return resetExpired || recipeExpired
}

// Interval returns the duration of the recipe interval.
func (r *Recipe) Interval() time.Duration {
	return *recipeInterval
}
