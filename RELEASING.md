# Releasing autotel-go

Go modules are published by pushing a semantic-version tag. The tag must match
the `/v2` module path declared in `go.mod`.

## Release checklist

1. Update `CHANGELOG.md`, `version.go`, the version test, and README version
   metadata to the intended release.
2. Run:

   ```sh
   go mod tidy -diff
   go build ./...
   go vet ./...
   go test -race ./...
   govulncheck ./...
   ```

3. Merge the release branch into `main` and create an annotated tag from the
   merge commit:

   ```sh
   git tag -a v2.1.0 -m "Release v2.1.0"
   git push origin main v2.1.0
   git push gitlab main v2.1.0
   ```

4. Verify that a clean consumer can resolve the published module:

   ```sh
   go list -m github.com/jagreehal/autotel-go/v2@v2.1.0
   ```

5. Create the GitHub release from the matching changelog section. Do not move
   or recreate a published tag; issue a new patch release if a correction is
   needed.
