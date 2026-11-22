package instagram

import (
	"context"
	"fmt"
)

func DebugPrintContext(username string) {
	body, status, err := getBody(context.Background(), "https://www.instagram.com/"+username+"/embed/", crawlerUA, "https://www.instagram.com/"+username+"/")
	if err != nil { fmt.Println("err", err); return }
	fmt.Println("status", status)
	c, err := extractContext(body)
	if err != nil { fmt.Println("ctx err", err); return }
	for k, v := range c {
		fmt.Printf("%s: %T %v\n", k, v, v)
	}
}
