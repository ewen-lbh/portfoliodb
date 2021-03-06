package main

import (
	"regexp"
	"strings"
	"unicode/utf8"

	"gopkg.in/yaml.v2"

	"github.com/anaskhan96/soup"
	"github.com/gomarkdown/markdown"
	"github.com/gomarkdown/markdown/parser"

	"github.com/metal3d/go-slugify"
)

const (
	patternLanguageMarker         string = `^::\s+(.+)$`
	patternAbbreviationDefinition string = `^\s*\*\[([^\]]+)\]:\s+(.+)$`
	mediaEmbedAttributeLooped     rune   = '~'
	mediaEmbedAttributeAutoplay   rune   = '>'
	mediaEmbedAttributeNoControls rune   = '='
)

// ParseYAMLHeader parses the YAML header of a description markdown file and returns
// the rest of the content (all except the YAML header)
func ParseYAMLHeader(descriptionRaw string) (map[string]interface{}, string) {
	var inYAMLHeader bool
	var rawYAMLPart string
	var markdownPart string
	for _, line := range strings.Split(descriptionRaw, "\n") {
		// if strings.TrimSpace(line) == "" && !inYAMLHeader {
		// 	continue
		// }
		if strings.TrimSpace(line) == "---" {
			inYAMLHeader = !inYAMLHeader
			continue
		}
		if inYAMLHeader {
			rawYAMLPart += line + "\n"
		} else {
			markdownPart += line + "\n"
		}
	}
	var parsedYAMLPart map[string]interface{}
	yaml.Unmarshal([]byte(rawYAMLPart), &parsedYAMLPart)
	if parsedYAMLPart == nil {
		parsedYAMLPart = make(map[string]interface{})
	}
	return parsedYAMLPart, markdownPart
}

// ParseDescription parses the markdown string from a description.md file and returns a ParsedDescription
func ParseDescription(ctx RunContext, markdownRaw string) ParsedDescription {
	ctx.Status("Parsing description.md")
	metadata, markdownRaw := ParseYAMLHeader(markdownRaw)
	// notLocalizedRaw: raw markdown before the first language marker
	notLocalizedRaw, localizedRawBlocks := splitOnLanguageMarkers(markdownRaw)
	localized := len(localizedRawBlocks) > 0
	var allLanguages []string
	if localized {
		allLanguages = MapKeys(localizedRawBlocks)
	} else {
		allLanguages = make([]string, 1)
		allLanguages[0] = "default" // TODO: make this configurable
	}
	paragraphs := make(map[string][]Paragraph, 0)
	mediaEmbedDeclarations := make(map[string][]MediaEmbedDeclaration, 0)
	links := make(map[string][]Link, 0)
	title := make(map[string]string, 0)
	footnotes := make(map[string][]Footnote, 0)
	abbreviations := make(map[string][]Abbreviation, 0)
	for _, language := range allLanguages {
		// Unlocalized stuff appears the same in every language.
		raw := notLocalizedRaw
		if localized {
			raw += localizedRawBlocks[language]
		}
		title[language], paragraphs[language], mediaEmbedDeclarations[language], links[language], footnotes[language], abbreviations[language] = parseSingleLanguageDescription(raw)
	}
	return ParsedDescription{
		Metadata:               metadata,
		Paragraphs:             paragraphs,
		Links:                  links,
		Title:                  title,
		MediaEmbedDeclarations: mediaEmbedDeclarations,
		Footnotes:              footnotes,
	}
}

// Abbreviation represents an abbreviation declaration in a description.md file
type Abbreviation struct {
	Name       string
	Definition string
}

// Footnote represents a footnote declaration in a description.md file
type Footnote struct {
	Name    string
	Content string
}

// Paragraph represents a paragraph declaration in a description.md file
type Paragraph struct {
	ID      string
	Content string
}

// Link represents an (isolated) link declaration in a description.md file
type Link struct {
	ID    string
	Name  string
	Title string
	URL   string
}

// Work represents a complete work, with analyzed mediae
type Work struct {
	ID         string
	Metadata   map[string]interface{}
	Title      map[string]string
	Paragraphs map[string][]Paragraph
	Media      map[string][]Media
	Links      map[string][]Link
	Footnotes  map[string][]Footnote
}

// MediaEmbedDeclaration represents media embeds. (abusing the ![]() syntax to extend it to any file)
// Only stores the info extracted from the syntax, no filesystem interactions.
type MediaEmbedDeclaration struct {
	Alt        string
	Title      string
	Source     string
	Attributes MediaAttributes
}

// MediaAttributes stores which HTML attributes should be added to the media
type MediaAttributes struct {
	Looped      bool // Controlled with attribute character ~ (adds)
	Autoplay    bool // Controlled with attribute character > (adds)
	Muted       bool // Controlled with attribute character > (adds)
	Playsinline bool // Controlled with attribute character = (adds)
	Controls    bool // Controlled with attribute character = (removes)
}

// ParsedDescription represents a work, but without analyzed media. All it contains is information from the description.md file
type ParsedDescription struct {
	Metadata               map[string]interface{}
	Title                  map[string]string
	Paragraphs             map[string][]Paragraph
	MediaEmbedDeclarations map[string][]MediaEmbedDeclaration
	Links                  map[string][]Link
	Footnotes              map[string][]Footnote
}

// splitOnLanguageMarkers returns two values:
// 1. the text before any language markers
// 2. a map with language codes as keys and the content as values
func splitOnLanguageMarkers(markdownRaw string) (string, map[string]string) {
	lines := strings.Split(markdownRaw, "\n")
	pattern := regexp.MustCompile(patternLanguageMarker)
	currentLanguage := ""
	before := ""
	markdownRawPerLanguage := map[string]string{}
	for _, line := range lines {
		if pattern.MatchString(line) {
			currentLanguage = pattern.FindStringSubmatch(line)[1]
			markdownRawPerLanguage[currentLanguage] = ""
		}
		if currentLanguage == "" {
			before += line + "\n"
		} else {
			markdownRawPerLanguage[currentLanguage] += line + "\n"
		}
	}
	return before, markdownRawPerLanguage
}

// parseSingleLanguageDescription takes in raw markdown without language markers (called on splitOnLanguageMarker's output)
// and returns parsed arrays of structs that make up each language's part in ParsedDescription's maps
func parseSingleLanguageDescription(markdownRaw string) (string, []Paragraph, []MediaEmbedDeclaration, []Link, []Footnote, []Abbreviation) {
	markdownRaw = handleAltMediaEmbedSyntax(markdownRaw)
	htmlRaw := markdownToHTML(markdownRaw)
	htmlTree := soup.HTMLParse(htmlRaw)
	paragraphs := make([]Paragraph, 0)
	mediae := make([]MediaEmbedDeclaration, 0)
	links := make([]Link, 0)
	footnotes := make([]Footnote, 0)
	abbreviations := make([]Abbreviation, 0)
	for _, paragraph := range htmlTree.FindAll("p") {
		childrenCount := len(paragraph.Children())
		firstChild := paragraph.Children()[0]
		if childrenCount == 1 && firstChild.NodeValue == "img" {
			alt, title := extractTitleFromMediaAlt(firstChild.Attrs()["alt"])
			alt, attributes := extractAttributesFromAlt(alt)
			mediae = append(mediae, MediaEmbedDeclaration{
				Alt:        alt,
				Title:      title,
				Source:     firstChild.Attrs()["src"],
				Attributes: attributes,
			})
		} else if childrenCount == 1 && firstChild.NodeValue == "a" {
			links = append(links, Link{
				ID:    slugify.Marshal(firstChild.FullText()),
				Name:  innerHTML(firstChild),
				Title: firstChild.Attrs()["title"],
				URL:   firstChild.Attrs()["href"],
			})
		} else if RegexpMatches(patternAbbreviationDefinition, innerHTML(paragraph)) {
			groups := RegexpGroups(patternAbbreviationDefinition, innerHTML(paragraph))
			abbreviations = append(abbreviations, Abbreviation{
				Name:       groups[1],
				Definition: groups[2],
			})
		} else if RegexpMatches(patternLanguageMarker, innerHTML(paragraph)) {
			continue
		} else {
			paragraphs = append(paragraphs, Paragraph{
				ID:      paragraph.Attrs()["id"],
				Content: innerHTML(paragraph),
			})
		}
	}
	title := innerHTML(htmlTree.Find("h1"))
	for _, div := range htmlTree.FindAll("div") {
		if div.Attrs()["class"] == "footnotes" {
			for _, li := range div.FindAll("li") {
				footnotes = append(footnotes, Footnote{
					Name:    strings.TrimPrefix(li.Attrs()["id"], "fn:"),
					Content: innerHTML(li),
				})
			}
		}
	}
	processedParagraphs := make([]Paragraph, 0, len(paragraphs))
	for _, paragraph := range paragraphs {
		processedParagraphs = append(processedParagraphs, processParagraph(paragraph, abbreviations))
	}
	return title, processedParagraphs, mediae, links, footnotes, abbreviations
}

// handleAltMediaEmbedSyntax handles the >[...](...) syntax by replacing it in htmlRaw with ![...](...)
func handleAltMediaEmbedSyntax(markdownRaw string) string {
	pattern := regexp.MustCompile(`(?m)^>(\[[^\]]+\]\([^)]+\)\s*)$`)
	return pattern.ReplaceAllString(markdownRaw, "!$1")
}

func extractTitleFromMediaAlt(altAttribute string) (string, string) {
	alt, title := "", ""
	var inTitleDecl bool
	var prevRune rune
	// ideaseed “Ideaseed’s wordmark”
	for _, curRune := range altAttribute {
		if curRune == '“' && prevRune == ' ' {
			inTitleDecl = true
		} else if curRune == '”' {
			inTitleDecl = false
		} else if inTitleDecl {
			title += string(curRune)
		} else {
			alt += string(curRune)
		}
		prevRune = rune(curRune)
	}
	return strings.TrimSpace(alt), strings.TrimSpace(title)
}

func extractAttributesFromAlt(alt string) (string, MediaAttributes) {
	attrs := MediaAttributes{
		Controls: true, // Controls is added by default, others aren't
	}
	lastRune, _ := utf8.DecodeLastRuneInString(alt)
	// If there are no attributes in the alt string, the first (last in the alt string) will not be an attribute character.
	if !isMediaEmbedAttribute(lastRune) {
		return alt, attrs
	}
	returnedAlt := ""
	// We iterate backwards:
	// if there are attributes, they'll be at the end of the alt text separated by a space
	inAttributesZone := true
	for i := len([]rune(alt)) - 1; i >= 0; i-- {
		revChar := []rune(alt)[i]
		if revChar == ' ' && inAttributesZone {
			inAttributesZone = false
			continue
		}
		if inAttributesZone {
			if revChar == mediaEmbedAttributeAutoplay {
				attrs.Autoplay = true
				attrs.Muted = true
			} else if revChar == mediaEmbedAttributeLooped {
				attrs.Looped = true
			} else if revChar == mediaEmbedAttributeNoControls {
				attrs.Controls = false
				attrs.Playsinline = true
			}
		} else {
			// TODO better variable name
			returnedAlt = string(revChar) + returnedAlt
		}
	}
	return returnedAlt, attrs
}

func isMediaEmbedAttribute(char rune) bool {
	return char == mediaEmbedAttributeAutoplay || char == mediaEmbedAttributeLooped || char == mediaEmbedAttributeNoControls
}

// innerHTML returns the HTML string of what's _inside_ the given element, just like JS' `element.innerHTML`
func innerHTML(element soup.Root) string {
	var innerHTML string
	for _, child := range element.Children() {
		innerHTML += child.HTML()
	}
	return innerHTML
}

// markdownToHTML converts markdown markdownRaw into an HTML string
func markdownToHTML(markdownRaw string) string {
	//TODO: handle markdown extensions (need to take in a "config Configuration" parameter)
	extensions := parser.CommonExtensions | parser.Footnotes | parser.AutoHeadingIDs | parser.Attributes | parser.HardLineBreak
	return string(markdown.ToHTML([]byte(markdownRaw), parser.NewWithExtensions(extensions), nil))
}

// processParagraph processes the given Paragraph to replace abbreviations
func processParagraph(paragraph Paragraph, currentLanguageAbbreviations []Abbreviation) Paragraph {
	processed := paragraph.Content
	for _, abbreviation := range currentLanguageAbbreviations {
		var replacePattern = regexp.MustCompile(`\b` + abbreviation.Name + `\b`)
		processed = replacePattern.ReplaceAllString(paragraph.Content, "<abbr title=\""+abbreviation.Definition+"\">"+abbreviation.Name+"</abbr>")
	}

	return Paragraph{Content: processed}
}
