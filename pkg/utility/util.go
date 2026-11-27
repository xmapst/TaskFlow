package utility

import (
	"os"
)

func FileOrPathExist(filename string) bool {
	_, err := os.Stat(filename)
	return err == nil || os.IsExist(err)
}

func EnsureDirExist(name string) error {
	if !FileOrPathExist(name) {
		return os.MkdirAll(name, os.ModePerm)
	}
	return nil
}

func CheckDuplicate[T comparable](slice []T) []T {
	var dup []T
	seen := make(map[T]struct{})
	for _, v := range slice {
		if _, exists := seen[v]; exists {
			dup = append(dup, v)
			continue
		}
		seen[v] = struct{}{}
	}
	if len(dup) == 0 {
		return nil
	}
	return dup
}

func RemoveDuplicate[T comparable](slice []T) []T {
	allKeys := make(map[T]bool)
	var list []T
	for _, item := range slice {
		if _, value := allKeys[item]; !value {
			allKeys[item] = true
			list = append(list, item)
		}
	}

	return list
}
