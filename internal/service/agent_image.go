package service

import "strings"

func (s *Service) resolveAgentImage(image string) string {
	return resolveAgentImageRef(image, s.cfg.AgentImagePrefix)
}

func resolveAgentImageRef(image, prefix string) string {
	image = strings.TrimSpace(image)
	prefix = strings.Trim(strings.TrimSpace(prefix), "/")
	if image == "" || prefix == "" || isRegistryQualifiedImageRef(image) {
		return image
	}
	return prefix + "/" + image
}

func isRegistryQualifiedImageRef(image string) bool {
	first, _, hasSlash := strings.Cut(image, "/")
	if !hasSlash {
		return false
	}
	return first == "localhost" || strings.Contains(first, ".") || strings.Contains(first, ":")
}
