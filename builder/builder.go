package builder

import (
	"context"
	"fmt"
	"path"

	"github.com/ricardozanini/kogito-builder/api"
	"github.com/ricardozanini/kogito-builder/client"
	"github.com/ricardozanini/kogito-builder/util"
	"github.com/ricardozanini/kogito-builder/util/log"
)

type resource struct {
	Target  string
	Content []byte
}

type buildContext struct {
	client.Client
	C         context.Context
	Build     *api.Build
	BaseImage string
}

type builder struct {
	L       log.Logger
	Context buildContext
}

type scheduler struct {
	builder
	Resources []resource
}

var _ Scheduler = &scheduler{}
var _ Builder = &builder{}

// Scheduler provides an interface to add resources and schedule a new build
type Scheduler interface {
	WithResource(target string, content []byte) Scheduler
	WithClient(client client.Client) Scheduler
	Schedule() (*api.Build, error)
}

type Builder interface {
	WithClient(client client.Client) Builder
	CancelBuild() (*api.Build, error)
	Reconcile() (*api.Build, error)
}

func FromBuild(build *api.Build) Builder {
	// TODO: verify Build integrity
	return &builder{
		L: log.WithName(util.ComponentName),
		Context: buildContext{
			Build: build,
			C:     context.TODO(),
		},
	}
}

// NewBuild is the API entry for the BuilderScheduler. Create a new Build instance based on PlatformBuild.
func NewBuild(platformBuild api.PlatformBuild, publishImage string, buildName string) Scheduler {
	// TODO: Figure if we need a PlatformBuild builder fluent api.
	// TODO: Verify structure integrity
	ctx := buildContext{
		BaseImage: platformBuild.Spec.BaseImage,
		C:         context.TODO(),
	}

	// TODO: Improve this check with factories instead
	if platformBuild.Spec.BuildStrategy == api.BuildStrategyPod && platformBuild.Spec.PublishStrategy == api.PlatformBuildPublishStrategyKaniko {
		ctx.Build = &api.Build{
			Spec: api.BuildSpec{
				Tasks: []api.Task{
					{Kaniko: &api.KanikoTask{
						BaseTask: api.BaseTask{Name: "KanikoTask"},
						PublishTask: api.PublishTask{
							ContextDir: path.Join("/builder", buildName, "context"),
							BaseImage:  platformBuild.Spec.BaseImage,
							Image:      publishImage,
							Registry:   platformBuild.Spec.Registry,
						},
						Cache: api.KanikoTaskCache{},
					}},
				},
				Strategy: api.BuildStrategyPod,
				Timeout:  *platformBuild.Spec.Timeout,
			},
		}
		ctx.Build.Name = buildName
		ctx.Build.Namespace = platformBuild.Namespace
	} else {
		panic(fmt.Errorf("BuildStrategy %s with PublishStrategy %s is not supported", platformBuild.Spec.BuildStrategy, platformBuild.Spec.PublishStrategy))
	}

	return &scheduler{
		builder: builder{
			L:       log.WithName(util.ComponentName),
			Context: ctx,
		},
		Resources: make([]resource, 0),
	}
}

func (b *scheduler) WithClient(client client.Client) Scheduler {
	b.builder.WithClient(client)
	return b
}

func (b *scheduler) WithResource(target string, content []byte) Scheduler {
	b.Resources = append(b.Resources, resource{target, content})
	return b
}

// Schedule schedules a new build in the platform
func (b *scheduler) Schedule() (*api.Build, error) {
	// TODO: create a handler to mount the resources according to the platform/context options (for now we only have CM, PoC level)
	if err := mountResourcesWithConfigMap(&b.Context, &b.Resources); err != nil {
		return nil, err
	}
	return b.Reconcile()
}

func (b *builder) WithClient(client client.Client) Builder {
	b.Context.Client = client
	return b
}

// Reconcile idempotent build flow control.
// Can be called many times to check/update the current status of the build instance, indexed by the Platform and Build Name.
func (b *builder) Reconcile() (*api.Build, error) {
	var actions []Action
	switch b.Context.Build.Spec.Strategy {
	case api.BuildStrategyPod:
		// build the action flow:
		actions = []Action{
			newInitializePodAction(),
			newScheduleAction(),
			newMonitorPodAction(),
			newErrorRecoveryAction(),
		}
	}

	target := b.Context.Build.DeepCopy()

	for _, a := range actions {
		a.InjectLogger(b.L)
		a.InjectClient(b.Context.Client)

		if a.CanHandle(target) {
			b.L.Infof("Invoking action %s", a.Name())
			newTarget, err := a.Handle(b.Context.C, target)
			if err != nil {
				b.L.Errorf(err, "Failed to invoke action %s", a.Name())
				return nil, err
			}

			if newTarget != nil {
				if newTarget.Status.Phase != target.Status.Phase {
					b.L.Info(
						"state transition",
						"phase-from", target.Status.Phase,
						"phase-to", newTarget.Status.Phase,
					)
				}

				target = newTarget
			}

		}
	}

	return target, nil

}

func (b *builder) CancelBuild() (*api.Build, error) {
	//TODO implement me
	panic("implement me")
}
