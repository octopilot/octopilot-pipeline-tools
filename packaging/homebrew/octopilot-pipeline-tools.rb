# Homebrew Formula for octopilot-pipeline-tools (op / octopipeline CLI).
# To use: copy to your Homebrew tap, e.g. homebrew-octopilot/Formula/o/octopilot-pipeline-tools.rb
# Update version and sha256 when cutting a release:
#   export VERSION=0.1.0
#   curl -sL "https://github.com/octopilot/octopilot-pipeline-tools/archive/refs/tags/v${VERSION}.tar.gz" | shasum -a 256
# Then run: brew update-python-resources octopilot-pipeline-tools  # to refresh dependency resources
class OctopilotPipelineTools < Formula
  include Language::Python::Virtualenv

  desc "CLI for Skaffold/Buildpacks pipelines: build, push, build_result.json, watch-deployment"
  homepage "https://github.com/octopilot/octopilot-pipeline-tools"
  url "https://github.com/octopilot/octopilot-pipeline-tools/archive/refs/tags/v0.1.0.tar.gz"
  sha256 "REPLACE_WITH_SHA256_AFTER_RELEASE"
  license "Apache-2.0"

  depends_on "python@3.12"

  def install
    venv = virtualenv_create(libexec, "python3.12")
    venv.pip_install buildpath
    bin.install_symlink libexec/"bin/op" => "op"
    bin.install_symlink libexec/"bin/octopipeline" => "octopipeline"
  end

  test do
    assert_match "Usage:", shell_output("#{bin}/op --help", 1)
  end
end
