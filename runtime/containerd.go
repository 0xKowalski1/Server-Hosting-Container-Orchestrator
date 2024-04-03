package runtime

import (
	"context"
	"fmt"
	"github.com/containerd/containerd"
	"github.com/containerd/containerd/namespaces"
	"log"
	"syscall"
	"time"

	"github.com/containerd/containerd/cio"
	"github.com/containerd/containerd/containers"
	"github.com/containerd/containerd/oci"
	"github.com/opencontainers/runtime-spec/specs-go"
)

// ContainerdRuntime implements the Runtime interface for containerd.
type ContainerdRuntime struct {
	client *containerd.Client
}

// NewContainerdRuntime creates a new instance of ContainerdRuntime with the given containerd client.
func NewContainerdRuntime(socketPath string) (*ContainerdRuntime, error) {
	client, err := containerd.New(socketPath)

	if err != nil {
		return nil, err
	}

	return &ContainerdRuntime{
		client: client,
	}, nil
}

// CreateContainer instantiates a new container but does not start it.
func (_runtime *ContainerdRuntime) CreateContainer(namespace string, config ContainerConfig) (Container, error) {
	ctx := namespaces.WithNamespace(context.Background(), namespace)

	image, err := _runtime.client.Pull(ctx, config.Image, containerd.WithPullUnpack)
	if err != nil {
		log.Printf("Error pulling image: %v", err)
		return Container{}, err
	}

	cont, err := _runtime.client.NewContainer(ctx, config.ID, containerd.WithImage(image), containerd.WithNewSnapshot(config.ID+"-snapshot", image), containerd.WithNewSpec(
		oci.WithHostNamespace(specs.NetworkNamespace),
		oci.WithImageConfig(image),
		oci.WithEnv(config.Env),
		func(ctx context.Context, _ oci.Client, _ *containers.Container, s *oci.Spec) error {
			//temporary
			s.Mounts = append(s.Mounts, specs.Mount{
				Destination: "/etc/resolv.conf",
				Type:        "bind",
				Source:      "/home/kowalski/dev/container-orchestrator/resolv.conf",
				Options:     []string{"rbind", "ro"},
			})

			return nil
		},
	))

	if err != nil {
		log.Printf("Error creating container: %v", err)
		return Container{}, err
	}

	return Container{ID: cont.ID()}, nil
}

// StartContainer starts an existing container.
func (_runtime *ContainerdRuntime) StartContainer(namespace string, containerID string) error {
	ctx := namespaces.WithNamespace(context.Background(), namespace)

	container, err := _runtime.client.LoadContainer(ctx, containerID)
	if err != nil {
		log.Printf("Failed to load container %s: %v", containerID, err)
		return err
	}

	logPath := "/home/kowalski/dev/container-orchestrator/log.log"

	task, err := container.NewTask(ctx, cio.LogFile(logPath))
	if err != nil {
		log.Printf("Failed to create task for container %s: %v", containerID, err)
		return err
	}
	defer task.Delete(ctx)

	if err := task.Start(ctx); err != nil {
		log.Printf("Failed to start task for container %s: %v", containerID, err)

		return err
	}

	log.Printf("Successfully started container %s", containerID)

	return nil
}

// StopContainer stops a running container.
func (_runtime *ContainerdRuntime) StopContainer(namespace string, containerID string, timeout int) error {
	ctx := namespaces.WithNamespace(context.Background(), namespace)

	container, err := _runtime.client.LoadContainer(ctx, containerID)
	if err != nil {
		log.Printf("Failed to load container %s: %v", containerID, err)
		return err
	}

	task, err := container.Task(ctx, cio.Load)
	if err != nil {
		log.Printf("failed to load task for container  %s: %v", containerID, err)
		return err
	}

	// Kill the task using SIGTERM, allowing for graceful shutdown
	// Not sure this is working
	if err := task.Kill(ctx, syscall.SIGTERM); err != nil {
		log.Printf("failed to send SIGTERM to container %s: %v", containerID, err)
		return err
	}

	exitCh, err := task.Wait(ctx)
	if err != nil {
		return fmt.Errorf("failed to wait on task for container %s: %v", containerID, err)
	}

	select {
	case <-exitCh:
		log.Printf("Container %s stopped gracefully", containerID)
	case <-time.After(time.Duration(timeout) * time.Second):
		log.Printf("Container %s stop timeout; sending SIGKILL", containerID)
		if err := task.Kill(ctx, syscall.SIGKILL); err != nil { // Forcefully stop the container
			log.Printf("failed to send SIGKILL to container %s: %v", containerID, err)
			return err
		}
		<-exitCh // Wait for the SIGKILL to take effect
	}

	if _, err := task.Delete(ctx); err != nil {
		log.Printf("failed to delete task for container %s: %v", containerID, err)
		return err
	}

	log.Printf("Successfully stopped container %s", containerID)

	return nil
}

// RemoveContainer removes a container from the system. This may require the container to be stopped first.
func (_runtime *ContainerdRuntime) RemoveContainer(namespace string, containerID string) error {
	ctx := namespaces.WithNamespace(context.Background(), namespace)

	container, err := _runtime.client.LoadContainer(ctx, containerID)
	if err != nil {
		log.Printf("Failed to load container %s: %v", containerID, err)
		return err
	}

	// Attempt to delete the container. If the container has a running task, containerd will return an error.
	if err := container.Delete(ctx, containerd.WithSnapshotCleanup); err != nil {
		log.Printf("Failed to delete container %s: %v", containerID, err)
		return err
	}

	log.Printf("Successfully deleted container %s", containerID)

	return nil
}

// ListContainers returns a list of all containers managed by the runtime.
func (_runtime *ContainerdRuntime) ListContainers(namespace string) ([]Container, error) {
	var containers []Container
	ctx := namespaces.WithNamespace(context.Background(), namespace)

	// List containers from containerd
	conts, err := _runtime.client.Containers(ctx)
	if err != nil {
		log.Printf("Error listing containers: %v", err)
		return nil, err
	}

	// Map containerd containers to generic Container struct
	for _, cont := range conts {
		/*info*/ _, err := cont.Info(ctx)
		if err != nil {
			log.Printf("Error getting container info: %v", err)
			continue
		}

		containers = append(containers, Container{
			ID: cont.ID(),
		})
	}

	return containers, nil
}

// GetContainerLogs returns the logs for a specific container.
//func GetContainerLogs(ctx context.Context, containerID string) (io.ReadCloser, error) {}

// InspectContainer returns detailed information about a specific container.
//func InspectContainer(ctx context.Context, containerID string) (Container, error) {}
