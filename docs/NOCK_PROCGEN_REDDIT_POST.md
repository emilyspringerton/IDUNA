# We wanted to compile arbitrary, possibly LLM-written code into textures on our server. Here's how we made that not a terrible idea.

We've been building a small in-house tool called **NOCK** — a procedural texture generator and library for our game projects. The interesting part isn't the layer stack or the SQLite-backed asset library, it's the compiler pipeline underneath: you write (or an LLM writes) a short program describing a texture, and it gets compiled and executed on our backend to produce a real PNG. That sentence should set off alarm bells for anyone who's thought about sandboxing user-supplied code, and it did for us too. Here's the trick we ended up using: **we sandboxed it by choosing which compiler backend to target, not by building a runtime sandbox at all.**

## The setup

We already have an in-house language, **PARENA** — an S-expression-syntax language with a real multi-target compiler: it can emit C, TypeScript, and (the relevant one here) Java source. It was originally built for game logic and editor plugins, nothing to do with graphics.

The idea for NOCK was simple: describe a texture as a small pure function — some combination of noise, gradients, trig, thresholding — evaluated once per pixel coordinate, and let the compiler do the heavy lifting instead of hand-writing a shader-ish DSL from scratch. Since we already had a real compiler lying around, this was "just" a matter of pointing it at texture generation instead of game code.

The scary part: we wanted this to run **server-side**, and we wanted to eventually let an LLM generate the source (type "cracked stone with moss in the seams," get back real generator code). That's about as adversarial an input model as it gets — untrusted, possibly-malicious-by-accident-or-design code, executed on a machine we care about.

## Why "just sandbox it" wasn't good enough

Our first instinct was the obvious one: run it in a container, drop privileges, seccomp filter, the usual playbook. All of that works, but it's a runtime property you have to get exactly right and keep getting right forever. One misconfigured mount, one seccomp rule you forgot, one library the sandbox didn't account for, and the whole guarantee is gone. It's also just *a lot* of moving parts for what is conceptually a pure math function that should never need to touch a filesystem or a socket in the first place.

Then we actually looked at our own compiler's C backend, because that was the obvious first target to reach for. It has a real, intentional escape hatch:

```
#target {:c (inline-c "...")}
```

This lets PARENA code declare a function whose body is literally raw C, taken verbatim. It exists for good reasons — FFI, talking to a host application, things a pure language genuinely can't express on its own. Our own compiler source code says the honest thing about it:

> *"the inline-c string is trusted verbatim as real C (the compiler has no way to check it)"*

For hand-written game logic living in our own repo, that's a completely reasonable trust boundary. For **generated texture code from an LLM, running on our infrastructure**, that same escape hatch is a full remote-code-execution primitive with a bow on it. Any compromised or merely *unlucky* generation could emit `#target {:c (inline-c "system(\"...\")")}` and we'd compile and run it. Runtime sandboxing can contain that. It would be much nicer to not have it be possible at all.

## The actual trick: target the JVM instead

We already had a second backend that nobody had thought of as a security boundary: the Java emitter. It was built for an unrelated reason (an Android app needed some simple decision logic compiled from PARENA), and it is deliberately, aggressively narrow in scope. Reading straight from its own header comment:

> *a `defn` with zero or more scalar (I32/F64/Bool/String) parameters, no region annotations, a body that is a SINGLE real expression — number/symbol literals, the same real binop set, `if` as a ternary, calls to another top-level defn or a recognized math primitive*

And the entire external surface it exposes is five functions:

```
math/random-f64  -> Math.random
math/floor       -> Math.floor
math/sqrt        -> Math.sqrt
math/log         -> Math.log
math/cos         -> Math.cos
```

That's it. There is no `#target`, no inline-anything, no FFI declaration of any kind anywhere in that emitter. We went and grepped to be sure rather than take our own word for it — zero matches. There's no `import`, no string formatting beyond what's already listed, no reflection, obviously no arbitrary bytecode injection surface, because the emitter doesn't have a code path that *could* emit any of that even if the input tried to ask for it.

This is the part worth writing up on its own: **the safety property isn't "we caught the dangerous operations," it's "the target language's own emitter has no way to express a dangerous operation in the first place."** It's not a permission system with rules to keep correct — it's closer to how a pure functional core with no effect system works, except enforced at the level of "the code generator literally does not know how to print that." A compiled PARENA→Java texture function is provably, structurally incapable of touching a file, opening a socket, or spawning a process, because none of those operations have any representation in the AST this backend accepts, let alone a lowering to Java for them.

The honest caveat, because this sub will ask: there's no loop construct, either — a function body is one expression. Recursion is legal (a `defn` can call itself), so it's not that the language is too weak to iterate, it's that whatever you compute, the worst realistic outcome is burning CPU or blowing the JVM stack on unbounded recursion — not exfiltrating anything, because the exfiltration primitives simply don't exist in this emission target. That's a meaningfully different (and much easier to reason about) failure mode than "did we configure the sandbox correctly."

## The pipeline

Once we trusted the target, the actual execution flow is almost boringly simple:

1. Real or generated PARENA source describing the texture function comes in.
2. We do a cheap defense-in-depth text check first anyway — reject any `#target` or non-`math/` import outright, even though the Java backend can't act on one if it slipped through. Belt and suspenders costs nothing.
3. `parena build --target java -o Texture.java` — compiles it to real, readable Java source.
4. `javac Texture.java` — compiles that against a small, fixed, **trusted** harness class we wrote by hand: it calls the generated static method once per (x, y) pixel coordinate (mapped into whatever domain the texture function expects), and writes the resulting values into a PNG buffer.
5. `java` runs it, we get pixel bytes back.

Nothing about the untrusted part of this pipeline ever runs outside the JVM's own type system, and the JVM never gets handed anything the compiler could lower into an I/O call.

## Two real bugs we hit building this (the unglamorous part)

Since this is r/proceduralgeneration and not r/programminghumor, the two dumbest things that actually cost us time:

- **`javac` and `java` from two different JDKs.** Our build box has a headless JRE with no `javac` at all, and a separate real JDK elsewhere on disk. Resolving each binary independently (falling back to `$PATH` for each) picked `javac` from the real JDK and `java` from the system JRE — different class file versions, instant `UnsupportedClassVersionError`. Fix: resolve both from the same JDK's `bin/` directory, always.
- **ImageMagick's `-crop` remembers where it came from.** `convert x.png -crop 1x1+X+Y -flatten txt:-` to sample a pixel silently sampled the *wrong* pixel, because `-crop` preserves the source image's virtual canvas offset unless you follow it with `+repage`. Cost us a genuinely confusing afternoon before we found the one-flag fix.

## Where this goes next

The generation side (LLM writes the PARENA source from a text prompt, we compile+render it through this exact pipeline) is live. The part we're most interested in next is whether this "pick the compiler target as the trust boundary" trick generalizes past textures — anywhere you want to run short, generated, math-shaped programs against untrusted input, "does our compiler have a backend with no I/O surface" seems like a genuinely underused question to ask before reaching for a container.

Happy to answer questions about the compiler internals, the math side of the texture functions, or why we apparently have three other compiler backends now.
