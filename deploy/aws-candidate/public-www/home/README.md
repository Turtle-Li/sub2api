# TurtleRoute public homepage

Static source for the public `/` route. The [Caddyfile](../../Caddyfile) serves
`index.html` from its homepage volume and maps `/helpcenter/*` to the static
asset volume. The [v6 layout assets](../help/turtleroute-20261116-v6/) reuse
the [v5 images and catalog renderer](../help/turtleroute-20261106-v5/).
Committing this snapshot does not publish it.

The hero pairs a responsive starfield and occasional meteors with a
Codex desktop illustration. The masked rows are synthetic; the illustration
contains no actual session content. It is rendered in HTML and CSS and
reuses the homepage brand icon. API setup examples remain in the
developer section. The plaza reads the same-origin public
`/api/v1/model-plaza` endpoint and keeps quotes attached to their source
group.

The `#status` cards use `/api/v1/model-status` through
`../help/turtleroute-status-20261116-v12/`. GPT, Claude and DeepSeek appear
first; the existing order within each platform is preserved. Pricing and
availability are separate. See `docs/features/MODEL_AVAILABILITY_GROUPS.md` for
visibility, separate request/check statistics and scheduled-check controls.

The public group labels and featured model ordering are covered by:

```sh
node --test deploy/aws-candidate/public-www/home/*.test.mjs
```

For a separately approved static release, publish the versioned assets
before switching the HTML. Retain the previous HTML and assets for rollback.
