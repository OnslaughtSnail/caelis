package gitignorefilter

import corefilter "github.com/OnslaughtSnail/caelis/internal/gitignorefilter"

type FileSystem = corefilter.FileSystem
type Matcher = corefilter.Matcher

func New(fs FileSystem, root string) (*Matcher, error) {
	return corefilter.New(fs, root)
}

func NewForPath(fs FileSystem, path string) (*Matcher, error) {
	return corefilter.NewForPath(fs, path)
}
