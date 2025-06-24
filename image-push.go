package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2"
	"oras.land/oras-go/v2/content/oci"
	"oras.land/oras-go/v2/registry"
	"oras.land/oras-go/v2/registry/remote"
	"oras.land/oras-go/v2/registry/remote/auth"
	"oras.land/oras-go/v2/registry/remote/credentials"
)

func pushImage(log *log.Logger, appDir, image string) error {
	// time-box operations, generously but not infinite
	ctx, cancel := context.WithTimeout(context.Background(), 600*time.Second)
	defer cancel()

	ref, err := registry.ParseReference(image)
	if err != nil {
		return fmt.Errorf("invalid image reference: %s: %w", image, err)
	}
	if err = ref.Validate(); err != nil {
		return fmt.Errorf("invalid image reference: %w", err)
	}

	// use docker auth info
	authStore, err := credentials.NewStoreFromDocker(credentials.StoreOptions{})
	if err != nil {
		return fmt.Errorf("failed to load auth config: %w", err)
	}
	authClient := &auth.Client{
		Credential: authStore.Get,
	}

	targetRegistry, err := remote.NewRegistry(ref.Registry)
	if err != nil {
		return fmt.Errorf("failed to create registry for %s: %w", ref.Registry, err)
	}
	targetRegistry.Client = authClient

	targetRepo, err := targetRegistry.Repository(ctx, ref.Repository) // remote.NewRepository(ref.Repository)
	if err != nil {
		return fmt.Errorf("failed to create repository for %s: %w", ref.Repository, err)
	}

	imagePath := filepath.Join(appDir, "image.tar")
	imageFile, err := os.Create(imagePath)
	if err != nil {
		return fmt.Errorf("failed to export image: create: %w", err)
	}

	defer os.Remove(imagePath)

	log.Print("exporting image to ", imageFile.Name())
	cmd := exec.CommandContext(ctx, "docker", "save", image)
	cmd.Stdout = imageFile
	cmd.Stderr = log.Writer()
	if err = cmd.Run(); err != nil {
		return fmt.Errorf("failed to export image: save: %w", err)
	}

	if err = imageFile.Close(); err != nil {
		return fmt.Errorf("failed to export image: close: %w", err)
	}

	imageStore, err := oci.NewFromTar(ctx, imagePath)
	if err != nil {
		return fmt.Errorf("failed to create OCI image store: %w", err)
	}

	ctx = auth.AppendRepositoryScope(ctx, ref, auth.ActionPull, auth.ActionPush)

	opts := oras.DefaultCopyOptions
	opts.PreCopy = func(ctx context.Context, desc ocispec.Descriptor) error {
		log.Print("- pushing ", desc)
		return nil
	}
	opts.OnCopySkipped = func(ctx context.Context, desc ocispec.Descriptor) error {
		log.Print("- skipped ", desc)
		return nil
	}

	desc, err := oras.Copy(ctx, imageStore, ref.Reference, targetRepo, ref.Reference, oras.DefaultCopyOptions)
	if err != nil {
		return fmt.Errorf("image push failed: %w", err)
	}

	log.Printf("image pushed: %s (digest: %s)", image, desc.Digest)
	return nil
}
