package builder

import (
	"context"
	"strings"

	"github.com/kiegroup/container-builder/api"
	"github.com/kiegroup/container-builder/client"
	"github.com/kiegroup/container-builder/util/defaults"
	"github.com/kiegroup/container-builder/util/minikube"
	"github.com/kiegroup/container-builder/util/registry"
	corev1 "k8s.io/api/core/v1"
)

var (
	gcrKanikoRegistrySecret = registrySecret{
		fileName:    "kaniko-secret.json",
		mountPath:   "/secret",
		destination: "kaniko-secret.json",
		refEnv:      "GOOGLE_APPLICATION_CREDENTIALS",
	}
	plainDockerKanikoRegistrySecret = registrySecret{
		fileName:    "config.json",
		mountPath:   "/kaniko/.docker",
		destination: "config.json",
	}
	standardDockerKanikoRegistrySecret = registrySecret{
		fileName:    corev1.DockerConfigJsonKey,
		mountPath:   "/kaniko/.docker",
		destination: "config.json",
	}

	kanikoRegistrySecrets = []registrySecret{
		gcrKanikoRegistrySecret,
		plainDockerKanikoRegistrySecret,
		standardDockerKanikoRegistrySecret,
	}
)

func addKanikoTaskToPod(ctx context.Context, c client.Client, build *api.Build, task *api.KanikoTask, pod *corev1.Pod) error {
	// TODO: perform an actual registry lookup based on the environment
	if task.Registry.Address == "" {
		address, err := registry.GetRegistryAddress(ctx, c)
		if err != nil {
			return err
		}
		if address != nil {
			task.Registry.Address = *address
		} else {
			address, err := minikube.FindRegistry(ctx, c)
			if err != nil {
				return err
			}
			if address != nil {
				task.Registry.Address = *address
			}
		}
	}

	// TODO: verify how cache is possible
	// TODO: the PlatformBuild structure should be able to identify the Kaniko context. For simplicity, let's use a CM with `dir://`
	args := []string{
		"--dockerfile=Dockerfile",
		"--context=dir://" + task.ContextDir,
		"--destination=" + task.Registry.Address + "/" + task.Image,
		"--cache=true",
		"--cache-ttl=1h",
	}

	if task.Verbose != nil && *task.Verbose {
		args = append(args, "-v=debug")
	}

	affinity := &corev1.Affinity{}
	env := make([]corev1.EnvVar, 0)
	volumes := make([]corev1.Volume, 0)
	volumeMounts := make([]corev1.VolumeMount, 0)

	if task.Registry.Secret != "" {
		secret, err := getRegistrySecret(ctx, c, pod.Namespace, task.Registry.Secret, kanikoRegistrySecrets)
		if err != nil {
			return err
		}
		addRegistrySecret(task.Registry.Secret, secret, &volumes, &volumeMounts, &env)
	}

	if task.Registry.Insecure {
		args = append(args, "--insecure")
		args = append(args, "--insecure-pull")
	}

	// TODO: should be handled by a mount build context handler instead since we can have many possibilities
	if err := addResourcesToVolume(ctx, c, task.PublishTask, build, &volumes, &volumeMounts); err != nil {
		return err
	}

	env = append(env, proxyFromEnvironment()...)

	container := corev1.Container{
		Name:            strings.ToLower(task.Name),
		Image:           defaults.KanikoExecutorImage,
		ImagePullPolicy: corev1.PullIfNotPresent,
		Args:            args,
		Env:             env,
		WorkingDir:      task.ContextDir,
		VolumeMounts:    volumeMounts,
	}

	// We may want to handle possible conflicts
	pod.Spec.Affinity = affinity
	pod.Spec.Volumes = append(pod.Spec.Volumes, volumes...)
	pod.Spec.Containers = append(pod.Spec.Containers, container)

	return nil
}
