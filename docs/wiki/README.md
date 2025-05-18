# Dora Wiki Documentation

This directory contains the source files for the Dora Block Explorer wiki.

## Structure

- `Home.md` - Main wiki landing page with navigation
- `TOGAF-Documentation.md` - Architecture documentation (manually updated)
- `Getting-Started/` - Guides for new users
- `Architecture/` - System architecture documentation

## Updating the Wiki

### TOGAF Documentation

The TOGAF documentation is maintained in the `/TOGAF/architecture_documentation.md` file. To update the wiki:

1. Edit the source file: `/TOGAF/architecture_documentation.md`
2. Copy the updated content to the wiki:
   ```bash
   cp /TOGAF/architecture_documentation.md /docs/wiki/TOGAF-Documentation.md
   ```
3. Update the "Last Updated" date in `TOGAF-Documentation.md`
4. Commit and push your changes

### Other Documentation

Edit the relevant markdown files in this directory and commit your changes.

## Viewing the Wiki

The wiki can be viewed directly on GitHub by browsing the `docs/wiki` directory. For a better reading experience, consider using a markdown viewer or a static site generator like [MkDocs](https://www.mkdocs.org/) or [Docsify](https://docsify.js.org/).

## License

Content in this wiki is available under the same license as the main Dora project.
