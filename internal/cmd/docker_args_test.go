package cmd

import (
	"testing"

	"github.com/GoogleContainerTools/skaffold/v2/pkg/skaffold/schema/latest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDockerCLIArgsPassesBuildArgsInStableOrder(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "")
	pkg := "pricewhisperer_orders"
	bin := "orders"
	art := &latest.Artifact{
		ArtifactType: latest.ArtifactType{
			DockerArtifact: &latest.DockerArtifact{
				DockerfilePath: "docker/microservices/Dockerfile",
				BuildArgs: map[string]*string{
					"PACKAGE": &pkg,
					"BINARY":  &bin,
				},
			},
		},
	}

	args := dockerCLIArgs(art, "linux/amd64", "ttl.sh/x:1d", "/repo/docker/microservices/Dockerfile", "/repo", "")

	require.Equal(t, "/repo", args[len(args)-1])
	joined := make([]string, 0)
	for i := 0; i < len(args)-1; i++ {
		if args[i] == "--build-arg" {
			joined = append(joined, args[i+1])
		}
	}
	assert.Equal(t, []string{"BINARY=orders", "PACKAGE=pricewhisperer_orders"}, joined)
}

func TestDockerCLIArgsMountsGitHubTokenAsABuildSecret(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "token")
	art := &latest.Artifact{ArtifactType: latest.ArtifactType{DockerArtifact: &latest.DockerArtifact{}}}

	args := dockerCLIArgs(art, "linux/amd64", "ttl.sh/x:1d", "Dockerfile", ".", "")

	assert.Contains(t, args, "--secret")
	assert.Contains(t, args, "id=github_token,env=GITHUB_TOKEN")
}

func TestDockerCLIArgsOffersImageRegistryAsBuildArg(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "")
	art := &latest.Artifact{ArtifactType: latest.ArtifactType{DockerArtifact: &latest.DockerArtifact{}}}

	args := dockerCLIArgs(art, "linux/amd64", "ttl.sh/x:1d", "Dockerfile", ".", "us-docker.pkg.dev/pw-ctl/ci")
	assert.Contains(t, args, "OP_IMAGE_REGISTRY=us-docker.pkg.dev/pw-ctl/ci")

	// skaffold's own value wins
	own := "mirror.example/x"
	art.DockerArtifact.BuildArgs = map[string]*string{"OP_IMAGE_REGISTRY": &own}
	args = dockerCLIArgs(art, "linux/amd64", "ttl.sh/x:1d", "Dockerfile", ".", "us-docker.pkg.dev/pw-ctl/ci")
	assert.Contains(t, args, "OP_IMAGE_REGISTRY=mirror.example/x")
	assert.NotContains(t, args, "OP_IMAGE_REGISTRY=us-docker.pkg.dev/pw-ctl/ci")

	// unset: nothing added
	args = dockerCLIArgs(&latest.Artifact{ArtifactType: latest.ArtifactType{DockerArtifact: &latest.DockerArtifact{}}}, "linux/amd64", "ttl.sh/x:1d", "Dockerfile", ".", "")
	for _, a := range args {
		assert.NotContains(t, a, "OP_IMAGE_REGISTRY")
	}
}
