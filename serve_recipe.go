package main

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"strconv"
	"strings"
)

// A recipe is one piece of software set up one way, as a JSON file: built
// in under recipes/<kind>/<name>.json (hi_serve.md, Recipes). Kinds are
// code; recipes are data, and never shell commands.

//go:embed recipes
var builtinRecipeFiles embed.FS

// recipeFile is a recipe as written.
type recipeFile struct {
	Kind        string `json:"kind"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Model       struct {
		Repo     string `json:"repo"`
		Revision string `json:"revision"`
		Quant    string `json:"quant"`
	} `json:"model"`
	Alias   string                     `json:"alias"`
	Context int                        `json:"context"`
	Args    []string                   `json:"args"`
	On      map[string]json.RawMessage `json:"on"`
}

// recipePlace is how a recipe runs on one kind of rented hardware.
type recipePlace struct {
	Hardware string   `json:"hardware"`
	Args     []string `json:"args"`
	Tested   string   `json:"tested"`
}

// localProfile is how a recipe runs on this machine with one backend.
type localProfile struct {
	Image  string   `json:"image"`
	Need   string   `json:"need"`  // the whole memory it takes, GPU and host, measured
	Limit  string   `json:"limit"` // the box's cgroup limit, for the host side
	Args   []string `json:"args"`
	Tested string   `json:"tested"`
}

// serveKinds are the kinds hi serve knows; only llm is built.
var serveKinds = []string{"llm", "web", "files", "db"}

// serveVerbs share a list of names with the kinds, so a recipe or a
// served thing can't be named like either.
var serveVerbs = []string{"ls", "status", "logs", "stop", "start", "rm", "key", "detect", "recipes", "pull", "models", "help"}

func isServeWord(name string) bool {
	for _, word := range append(append([]string{}, serveKinds...), serveVerbs...) {
		if name == word {
			return true
		}
	}
	return false
}

// serveRecipes are the built-in llm recipes, by name.
var serveRecipes = mustLoadBuiltinRecipes()

func mustLoadBuiltinRecipes() map[string]serveRecipe {
	recipes, err := loadRecipes(builtinRecipeFiles, "recipes")
	if err != nil {
		panic(err)
	}
	return recipes
}

// loadRecipes reads every recipes/<kind>/<name>.json under root.
func loadRecipes(files fs.FS, root string) (map[string]serveRecipe, error) {
	recipes := map[string]serveRecipe{}
	err := fs.WalkDir(files, root, func(name string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() || path.Ext(name) != ".json" {
			return err
		}
		data, err := fs.ReadFile(files, name)
		if err != nil {
			return err
		}
		recipe, err := parseRecipe(data)
		if err != nil {
			return fmt.Errorf("recipe %s: %w", name, err)
		}
		if want := path.Join(root, "llm", recipe.name+".json"); name != want {
			return fmt.Errorf("recipe %s: an llm recipe named %s belongs in %s", name, recipe.name, want)
		}
		recipes[recipe.name] = recipe
		return nil
	})
	return recipes, err
}

var recipeNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]*$`)

func parseRecipe(data []byte) (serveRecipe, error) {
	var file recipeFile
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&file); err != nil {
		return serveRecipe{}, err
	}
	if file.Kind != "llm" {
		return serveRecipe{}, fmt.Errorf("kind %q isn't built yet", file.Kind)
	}
	if !recipeNamePattern.MatchString(file.Name) || isServeWord(file.Name) {
		return serveRecipe{}, fmt.Errorf("invalid name %q", file.Name)
	}
	if file.Model.Repo == "" || file.Model.Quant == "" || file.Context <= 0 || file.Alias == "" {
		return serveRecipe{}, fmt.Errorf("needs model.repo, model.quant, alias, and context")
	}
	recipe := serveRecipe{
		name: file.Name, description: file.Description, repo: file.Model.Repo, revision: file.Model.Revision,
		quant: file.Model.Quant, context: file.Context, alias: file.Alias, common: file.Args,
		hardware: map[string]string{}, rented: map[string][]string{}, tests: map[string]string{},
		local: map[string]localProfile{},
	}
	if err := checkRecipeArgs(file.Args); err != nil {
		return serveRecipe{}, err
	}
	for where, raw := range file.On {
		if where == "local" {
			decoder := json.NewDecoder(strings.NewReader(string(raw)))
			decoder.DisallowUnknownFields()
			var profiles map[string]localProfile
			if err := decoder.Decode(&profiles); err != nil {
				return serveRecipe{}, fmt.Errorf("on.local: %w", err)
			}
			for backend, profile := range profiles {
				if backend != "rocm" && backend != "vulkan" {
					return serveRecipe{}, fmt.Errorf("on.local: unknown backend %q", backend)
				}
				if !strings.Contains(profile.Image, "@sha256:") {
					return serveRecipe{}, fmt.Errorf("on.local.%s: the image must be pinned by digest", backend)
				}
				if _, err := parseSize(profile.Need); err != nil {
					return serveRecipe{}, fmt.Errorf("on.local.%s.need: %w", backend, err)
				}
				if _, err := parseSize(profile.Limit); err != nil {
					return serveRecipe{}, fmt.Errorf("on.local.%s.limit: %w", backend, err)
				}
				if err := checkRecipeArgs(profile.Args); err != nil {
					return serveRecipe{}, err
				}
				recipe.local[backend] = profile
			}
			continue
		}
		decoder := json.NewDecoder(strings.NewReader(string(raw)))
		decoder.DisallowUnknownFields()
		var place recipePlace
		if err := decoder.Decode(&place); err != nil {
			return serveRecipe{}, fmt.Errorf("on.%s: %w", where, err)
		}
		if err := checkRecipeArgs(place.Args); err != nil {
			return serveRecipe{}, err
		}
		if place.Hardware != "" {
			recipe.hardware[where] = place.Hardware
		}
		if len(place.Args) > 0 {
			recipe.rented[where] = place.Args
		}
		if place.Tested != "" {
			recipe.tests[where] = place.Tested
		}
	}
	return recipe, nil
}

// checkRecipeArgs refuses arguments that set where the server listens, its
// key, or its model: hi sets those.
func checkRecipeArgs(args []string) error {
	for _, arg := range args {
		name, _, _ := strings.Cut(arg, "=")
		switch name {
		case "--host", "--port", "--api-key", "--api-key-file", "-m", "--model", "-hf", "--hf-repo", "--path", "--ssl-key-file", "--ssl-cert-file":
			return fmt.Errorf("%s is set by hi, not by a recipe or --args", name)
		}
		if strings.ContainsAny(arg, "\n\x00") {
			return fmt.Errorf("invalid argument %q", arg)
		}
	}
	return nil
}

// forProvider sets the recipe's arguments and tested line for one rented
// provider: its own arguments first, then the recipe's, unless --args
// replaced them.
func (r serveRecipe) forProvider(provider string) serveRecipe {
	if !r.override {
		r.args = strings.Join(append(append([]string{}, r.rented[provider]...), r.common...), " ")
	}
	r.tested = r.tests[provider]
	if r.tested == "" {
		for _, name := range sortedKeys(r.tests) {
			r.tested = r.tests[name]
			break
		}
	}
	return r
}

// places lists where a recipe runs, such as colab, hf, and local (rocm).
func (r serveRecipe) places() []string {
	var places []string
	for _, provider := range sortedKeys(r.hardware) {
		places = append(places, provider)
	}
	for _, backend := range sortedKeys(r.local) {
		places = append(places, "local ("+backend+")")
	}
	return places
}

// parseSize reads a size such as 91G, 512M, or 72GB, in powers of 1024,
// as Podman does.
func parseSize(value string) (int64, error) {
	text := strings.ToUpper(strings.TrimSpace(value))
	text = strings.TrimSuffix(strings.TrimSuffix(text, "IB"), "B")
	multiplier := int64(1)
	if text != "" {
		switch text[len(text)-1] {
		case 'K':
			multiplier = 1 << 10
		case 'M':
			multiplier = 1 << 20
		case 'G':
			multiplier = 1 << 30
		case 'T':
			multiplier = 1 << 40
		}
		if multiplier > 1 {
			text = text[:len(text)-1]
		}
	}
	number, err := strconv.ParseFloat(text, 64)
	if err != nil || number <= 0 {
		return 0, fmt.Errorf("%q isn't a size such as 72G", value)
	}
	return int64(number * float64(multiplier)), nil
}

// sizeText is a size in GB, as hi serve prints them.
func sizeText(bytes int64) string {
	switch {
	case bytes >= 100<<30:
		return fmt.Sprintf("%d GB", (bytes+(1<<29))>>30)
	case bytes >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(bytes)/(1<<30))
	default:
		return fmt.Sprintf("%d MB", (bytes+(1<<19))>>20)
	}
}
