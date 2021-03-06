package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"time"
)

const (
	TEAMCITY_TIMESTAMP_FORMAT = "2006-01-02T15:04:05.000"
)

type Test struct {
	Start    string
	Name     string
	Output   string
	Details  []string
	Duration time.Duration
	Status   string
	Race     bool
	Suite    bool
}

var (
	input  = os.Stdin
	output = os.Stdout

	additionalTestName = ""

	run  = regexp.MustCompile("^=== RUN\\s+([a-zA-Z_]\\S*)")
	end  = regexp.MustCompile("^(\\s*)--- (PASS|SKIP|FAIL):\\s+([a-zA-Z_]\\S*) \\((-?[\\.\\ds]+)\\)")
	pkg  = regexp.MustCompile("^(ok|PASS|FAIL|exit status|Found)")
	race = regexp.MustCompile("^WARNING: DATA RACE")
)

func init() {
	flag.StringVar(&additionalTestName, "name", "", "Add prefix to test name")
}

func escapeLines(lines []string) string {
	return escape(strings.Join(lines, "\n"))
}

func escape(s string) string {
	s = strings.Replace(s, "|", "||", -1)
	s = strings.Replace(s, "\n", "|n", -1)
	s = strings.Replace(s, "\r", "|n", -1)
	s = strings.Replace(s, "'", "|'", -1)
	s = strings.Replace(s, "]", "|]", -1)
	s = strings.Replace(s, "[", "|[", -1)
	return s
}

func getNow() string {
	return time.Now().Format(TEAMCITY_TIMESTAMP_FORMAT)
}

func outputTest(w io.Writer, test *Test) {
	now := getNow()
	testName := escape(additionalTestName + test.Name)
	fmt.Fprintf(w, "##teamcity[testStarted timestamp='%s' name='%s' captureStandardOutput='true']\n", test.Start, testName)
	fmt.Fprint(w, test.Output)
	if test.Status == "SKIP" {
		fmt.Fprintf(w, "##teamcity[testIgnored timestamp='%s' name='%s']\n", now, testName)
	} else {
		if test.Race {
			fmt.Fprintf(w, "##teamcity[testFailed timestamp='%s' name='%s' message='Race detected!' details='%s']\n",
				now, testName, escapeLines(test.Details))
		} else {
			switch test.Status {
			case "FAIL":
				fmt.Fprintf(w, "##teamcity[testFailed timestamp='%s' name='%s' details='%s']\n",
					now, testName, escapeLines(test.Details))
			case "PASS":
				// ignore
			default:
				fmt.Fprintf(w, "##teamcity[testFailed timestamp='%s' name='%s' message='Test ended in panic.' details='%s']\n",
					now, testName, escapeLines(test.Details))
			}
		}
		fmt.Fprintf(w, "##teamcity[testFinished timestamp='%s' name='%s' duration='%d']\n",
			now, testName, test.Duration/time.Millisecond)
	}
}

func startSuite(w io.Writer, name string) {
	fmt.Fprintf(w, "##teamcity[testSuiteStarted name='%s']\n", escape(name))
}

func finishSuite(w io.Writer, name string) {
	fmt.Fprintf(w, "##teamcity[testSuiteFinished name='%s']\n", escape(name))
}

func suite(name string) string {
	if idx := strings.LastIndex(name, "/"); idx != -1 {
		return name[:idx]
	}
	return ""
}

func processReader(r *bufio.Reader, w io.Writer) {
	tests := map[string]*Test{}
	suites := []string{}
	var test *Test
	newTest := func(name string) *Test {
		t := &Test{
			Name:  name,
			Start: getNow(),
		}
		tests[t.Name] = t
		for n := suite(name); n != ""; n = suite(n) {
			if p := tests[n]; p != nil {
				p.Suite = true
			}
		}
		return t
	}
	var final string
	prefix := "\t"
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			break
		}

		runOut := run.FindStringSubmatch(line)
		endOut := end.FindStringSubmatch(line)
		pkgOut := pkg.FindStringSubmatch(line)

		if test != nil && test.Status != "" && (runOut != nil || endOut != nil || pkgOut != nil) {
			for j := len(suites) - 1; j >= 0; j-- {
				if !strings.HasPrefix(test.Name, suites[j]) {
					finishSuite(w, suites[j])
					suites = suites[:j]
				}
			}
			if test.Suite {
				startSuite(w, test.Name)
				suites = append(suites, test.Name)
			}
			outputTest(w, test)
			delete(tests, test.Name)
			test = nil
		}

		if runOut != nil {
			test = newTest(runOut[1])
		} else if endOut != nil {
			test = tests[endOut[3]]
			if test == nil {
				test = newTest(endOut[3])
			}
			prefix = endOut[1] + "\t"
			test.Status = endOut[2]
			test.Duration, _ = time.ParseDuration(endOut[4])
		} else if pkgOut != nil {
			final += line
		} else if test != nil && race.MatchString(line) {
			test.Race = true
		} else if test != nil && test.Status != "" && strings.HasPrefix(line, prefix) {
			line = line[:len(line)-1]
			line = strings.TrimPrefix(line, prefix)
			test.Details = append(test.Details, line)
		} else if test != nil {
			test.Output += line
		} else {
			fmt.Fprint(w, line)
		}
	}
	if test != nil {
		outputTest(w, test)
		delete(tests, test.Name)
	}
	for j := len(suites) - 1; j >= 0; j-- {
		finishSuite(w, suites[j])
	}
	for _, t := range tests {
		outputTest(w, t)
	}

	fmt.Fprint(w, final)
}

func main() {
	flag.Parse()

	if len(additionalTestName) > 0 {
		additionalTestName += " "
	}

	reader := bufio.NewReader(input)

	processReader(reader, output)
}
