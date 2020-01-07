package app

import (
	"sort"
	"strings"
)

func transformUserSet(userSet map[string]struct{}, userIDs []string, action string) map[string]struct{} {
	switch action {
	case "replace":
		userSet = make(map[string]struct{})
		fallthrough
	case "append":
		for _, userID := range userIDs {
			userSet[userID] = struct{}{}
		}
	case "remove":
		for _, userID := range userIDs {
			delete(userSet, userID)
		}
	}
	return userSet
}

func parseUserSet(userList string) map[string]struct{} {
	fields := strings.Split(userList, ",")
	userSet := make(map[string]struct{})
	for _, field := range fields {
		userSet[strings.TrimSpace(field)] = struct{}{}
	}
	return userSet
}

func marshalUserSet(userSet map[string]struct{}) string {
	userIDs := make([]string, 0, len(userSet))
	for userID := range userSet {
		userIDs = append(userIDs, userID)
	}
	sort.Strings(userIDs)
	return strings.Join(userIDs, ",")
}
