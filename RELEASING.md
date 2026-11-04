# Releasing autotel-go

You publish a Go module by pushing a semantic-version tag. The tag must match the `/v2` module path in `go.mod`.

1. Move the `[Unreleased]` entries in `CHANGELOG.md` into a section for the new version, and set `Version` in `version.go` and the version test to match.

2. Run the checks:

   ```sh
   go mod tidy -diff
   go build ./...
   go vet ./...
   go test -race ./...
   govulncheck ./...
   ```

3. Merge the release branch into `main`, then create an annotated tag from the merge commit:

   ```sh
   git tag -a v2.4.0 -m "Release v2.4.0"
   git push origin main v2.4.0
   ```

   The [release workflow](.github/workflows/release.yml) fails if the tag doesn't match `version.go`, if the changelog has no section for it, or if `go mod tidy -diff` or the tests fail.

4. Check that a clean consumer can resolve the published module:

   ```sh
   go list -m github.com/jagreehal/autotel-go/v2@v2.4.0
   ```

5. Create the GitHub release from the matching changelog section.

Never move or recreate a published tag. To correct a release, issue a new patch release.
