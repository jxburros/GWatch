# The GWatch agent skill

`SKILL.md` teaches an AI assistant how to use GWatch well through the `gwatch-mcp` MCP server:
where to start, how to drill into a problem, what the statuses mean, and how to be careful with
the write tools. It complements the tool descriptions the server itself provides; it does not
replace setting up the server (see [`mcp/README.md`](../mcp/README.md), or **Settings › AI & MCP**
in GWatch, which also offers this skill as a download).

## Installing it

**Claude Code** reads skills from a `skills` folder. Put the whole folder there, so the file
ends up at one of:

```
~/.claude/skills/gwatch/SKILL.md                 # for every project
<your project>/.claude/skills/gwatch/SKILL.md    # for one project
```

The zip GWatch offers already unpacks to a `gwatch/` folder, so unzipping it inside `skills/`
is the whole installation.

**Claude Desktop and other assistants** without a skills folder: paste the body of `SKILL.md`
(everything below the `---` front matter) into the assistant's custom instructions, a project's
instructions, or the start of the conversation.

## Versions

The skill has its own version, in `VERSION` and in the front matter of `SKILL.md`, independent
of GWatch and of `gwatch-mcp`. GWatch remembers when the skill was last downloaded from its
settings page and mentions, quietly, when a newer one is available.
