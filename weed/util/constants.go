package util

import (
	"fmt"
)

var (
	VERSION = fmt.Sprintf("%s %d.%02d", sizeLimit, 2, 55)
	COMMIT  = ""
)

func Version() string {
	return VERSION + " " + COMMIT
}
