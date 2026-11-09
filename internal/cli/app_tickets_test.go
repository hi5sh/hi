package cli

import (
	"fmt"
	"sync"
	"testing"
)

// Tasks added at once each get a number of their own, and none is lost.
func TestTeamLocalTicketsAddedAtOnce(t *testing.T) {
	setupTeamTest(t)
	newTeamTestBroker(t)
	var wait sync.WaitGroup
	for i := range 20 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			var result appTicketResult
			if err := appLocalTickets("payments", appTicketRequest{Action: "add", Title: fmt.Sprintf("task %d", i)}, &result); err != nil {
				t.Error(err)
			}
		}()
	}
	wait.Wait()
	tickets := loadTeamLocalTickets("payments")
	seen := map[int]bool{}
	for _, ticket := range tickets {
		seen[ticket.Number] = true
	}
	if len(tickets) != 20 || len(seen) != 20 {
		t.Fatalf("%d tasks, %d numbers", len(tickets), len(seen))
	}
}
