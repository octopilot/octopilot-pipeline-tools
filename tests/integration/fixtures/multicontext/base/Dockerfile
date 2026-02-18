# We need a base image that is compatible with the buildpack stack (Ubuntu Bionic usually for v1 builder)
# Or we can just use the official run image as a base and add something trivial
FROM gcr.io/buildpacks/gcp/run:v1
RUN apk add --no-cache git curl
RUN touch /base-image-marker
