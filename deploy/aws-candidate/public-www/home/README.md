# TurtleRoute public homepage

Static source for the public `/` route. The [Caddyfile](../../Caddyfile) serves
`index.html` from its homepage volume and maps `/helpcenter/*` to the static
asset volume. The [versioned assets](../help/turtleroute-20261106-v5/)
mirror that layout. Committing this snapshot does not publish it.

The hero uses a responsive starfield and occasional meteors. The platform
ticker and model plaza follow it. The plaza reads the same-origin public
`/api/v1/model-plaza` endpoint and keeps quotes attached to their source
group. The public group labels and featured model ordering are covered by:

```sh
node --test deploy/aws-candidate/public-www/home/catalog-pricing.test.mjs
```

For a separately approved static release, publish the versioned assets
before switching the HTML. Retain the previous HTML and assets for rollback.
