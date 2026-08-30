package main

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	z "github.com/Oudwins/zog"
)

type showsCreateArguments struct {
	Artwork  string
	Language string
	Title    string
	Slug     string
	Type     string
}

type episodesCreateArguments struct {
	Show        string
	Title       string
	Description *string
	File        string
}

var showSlugPattern = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

var showsCreateArgumentsSchema = z.Struct(z.Shape{
	titleFlagName: z.String().Trim().Required(z.Message("is required")).Min(1, z.Message("is required")),
	slugFlagName: z.String().Trim().Required(z.Message("is required")).Min(1, z.Message("is required")).Match(
		showSlugPattern,
		z.Message("must use lowercase letters, numbers, and single hyphens"),
	),
	typeFlagName: z.String().Trim().Required(z.Message("is required")).Min(1, z.Message("is required")).OneOf(
		[]string{"audio", "video"},
		z.Message("must be audio or video"),
	),
	languageFlagName: z.String().Trim().Required(z.Message("is required")).Min(1, z.Message("is required")).Match(
		regexp.MustCompile(`^[a-z]{2}(?:-[A-Z]{2})?$`),
		z.Message("must use a language code such as en or en-US"),
	),
})

var episodesCreateArgumentsSchema = z.Struct(z.Shape{
	showFlagName: z.String().Trim().Required(z.Message("is required")).Min(1, z.Message("is required")).Match(
		showSlugPattern,
		z.Message("must be a valid show slug"),
	),
	titleFlagName: z.String().Trim().Required(z.Message("is required")).Min(1, z.Message("is required")),
	descriptionFlagName: z.Ptr(
		z.String().Trim().Required(z.Message("must not be blank when provided")).Min(
			1,
			z.Message("must not be blank when provided"),
		),
	),
	fileFlagName: z.String().Trim().Required(z.Message("is required")).Min(1, z.Message("is required")),
})

type cliArgumentValidationError struct {
	command string
	issues  z.ZogIssueList
	order   []string
}

func (err *cliArgumentValidationError) Error() string {
	positions := make(map[string]int, len(err.order))
	for index, field := range err.order {
		positions[field] = index
	}
	issues := append(z.ZogIssueList(nil), err.issues...)
	sort.SliceStable(issues, func(i, j int) bool {
		return issuePosition(issues[i], positions) < issuePosition(issues[j], positions)
	})
	var output strings.Builder
	_, _ = fmt.Fprintf(&output, "%s: invalid arguments", err.command)
	for _, issue := range issues {
		_, _ = fmt.Fprintf(&output, "\n  --%s %s", issueField(issue), issue.Message)
	}
	return output.String()
}

func validateShowsCreateArguments(args *showsCreateArguments) error {
	issues := showsCreateArgumentsSchema.Validate(args)
	if len(issues) == 0 {
		return nil
	}
	return &cliArgumentValidationError{
		command: "shows create", issues: issues, order: []string{titleFlagName, slugFlagName, typeFlagName, languageFlagName},
	}
}

func validateEpisodesCreateArguments(args *episodesCreateArguments) error {
	issues := episodesCreateArgumentsSchema.Validate(args)
	if len(issues) == 0 {
		return nil
	}
	return &cliArgumentValidationError{
		command: "episodes create", issues: issues,
		order: []string{showFlagName, titleFlagName, descriptionFlagName, fileFlagName},
	}
}

func issuePosition(issue *z.ZogIssue, positions map[string]int) int {
	position, ok := positions[issueField(issue)]
	if !ok {
		return len(positions)
	}
	return position
}

func issueField(issue *z.ZogIssue) string {
	if issue == nil || len(issue.Path) == 0 {
		return "argument"
	}
	return issue.Path[0]
}
