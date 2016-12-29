package birc

import (
    "strings"
    "fmt"
)

func Tableformatter(nicks []string, nicksPerRow int, continued bool) string {
    result := "|IRC users"
    if continued {
        result = "|(continued)"
    }
    for i := 0; i < 2; i++ {
        for j := 1; j <= nicksPerRow && j <= len(nicks); j++ {
            if i == 0 {
                result += "|"
            } else {
                result += ":-|"
            }
        }
        result += "\r\n|"
    }
    result += nicks[0] + "|"
    for i := 1; i < len(nicks); i++ {
        if i%nicksPerRow == 0 {
            result += "\r\n|" + nicks[i] + "|"
        } else {
            result += nicks[i] + "|"
        }
    }
    return result
}

func Plainformatter(nicks []string, nicksPerRow int) string {
    return strings.Join(nicks, ", ") + " currently on IRC"
}

func IsMarkup(message string) bool {
    switch message[0] {
    case '|':
        fallthrough
    case '#':
        fallthrough
    case '_':
        fallthrough
    case '*':
        fallthrough
    case '~':
        fallthrough
    case '-':
        fallthrough
    case ':':
        fallthrough
    case '>':
        fallthrough
    case '=':
        return true
    }
    return false
}

func FormatNick(nick string) string {
    rcolors := []int{19, 20, 22, 24, 25, 26, 27, 28, 29}
    rcolors = append(rcolors[1:], rcolors[0])
    sum := 0
    for _, char := range nick {
        sum += int(char)
    }
    return fmt.Sprintf("\x03%d@%s\x0f", rcolors[sum % 9] - 16, nick)
}
