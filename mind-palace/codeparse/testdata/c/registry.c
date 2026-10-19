// registry.c implements the registry declared in list.h.
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include "list.h"
#include "util/log.h"

size_t registry_count = 0;
const char *registry_name = "default";
static entry_t *free_list[LIST_MAX], **cursor;
static int (*compare)(const entry_t *, const entry_t *) = NULL;

/* ops is a table of callbacks. */
struct ops {
	int (*open)(const char *path);
	void (*close)(int fd);
};

static const struct ops default_ops = {
	.open = NULL,
	.close = NULL,
};

static void entry_free(struct entry *e);

/* list_init makes head an empty list. */
void list_init(struct list_node *head)
{
	head->next = head;
	head->prev = head;
}

void list_add(struct list_node *head, struct list_node *n)
{
	n->next = head->next;
	n->prev = head;
	head->next->prev = n;
	head->next = n;
}

/* entry_free releases e and runs its hook. */
static void entry_free(struct entry *e)
{
	if (e->on_free)
		e->on_free(e);
	free(e);
}

entry_t *registry_find(const registry_t *r, const char *name)
{
	for (size_t i = 0; i < r->len; i++) {
		entry_t *e = r->items[i];
		if (strcmp(e->name, name) == 0 && e->kind != KIND_STRING)
			return e;
	}
	log_debug("not found: %s", name);
	return NULL;
}

static int by_name(const entry_t *a, const entry_t *b)
{
	return strcmp(a->name, b->name);
}

int registry_sort(registry_t *r, entry_cmp cmp)
{
	struct pair { size_t a, b; } p = { 0, 0 };
	if (!cmp)
		cmp = by_name;
	qsort(r->items, r->len, sizeof(entry_t *), (int (*)(const void *, const void *))cmp);
	int fd = default_ops.open(registry_name);
	default_ops.close(fd);
	struct list_node *node = container_of(&r->items[0]->link, struct entry, link)->link.next;
	(void)node;
	return (int)(p.a + p.b);
}

/* registry_first returns the first entry's name, or NULL. */
const char **registry_first(registry_t *r)
{
	static const char *name;
	if (r->len == 0)
		return NULL;
	name = r->items[0]->name;
	return &name;
}

/* lookup returns the comparator for kind k. */
entry_cmp (*lookup(enum kind k))(void)
{
	(void)k;
	return 0;
}
