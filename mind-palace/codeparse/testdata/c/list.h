/*
 * list.h - an intrusive doubly linked list and a small object registry.
 */
#ifndef LIST_H
#define LIST_H

#include <stddef.h>
#include <stdint.h>
#include "config.h"

#ifdef __cplusplus
extern "C" {
#endif

/* LIST_MAX bounds the number of nodes a registry holds. */
#define LIST_MAX 1024

/* container_of returns the structure that embeds member ptr. */
#define container_of(ptr, type, member) \
	((type *)((char *)(ptr) - offsetof(type, member)))

/** list_node links an entry into a list. */
struct list_node {
	struct list_node *next;
	struct list_node *prev;
};

/* value holds one of the payload kinds. */
union value {
	int64_t i;
	double d;
	const char *s;
};

/* kind tags a value. */
enum kind {
	KIND_INT,
	KIND_DOUBLE = 2,
	KIND_STRING,
};

/* entry is a registry entry. */
typedef struct entry {
	struct list_node link;
	enum kind kind;
	union value value;
	unsigned flags : 4;
	char name[32];
	void (*on_free)(struct entry *e);
} entry_t;

/* An anonymous struct behind a typedef. */
typedef struct {
	size_t len;
	entry_t **items;
} registry_t;

typedef int (*entry_cmp)(const entry_t *a, const entry_t *b);
typedef entry_t *entry_ptr;
typedef uint8_t digest_t[32];

/* registry_count is the number of live entries. */
extern size_t registry_count;
extern const char *registry_name;

/* list_init makes head an empty list. */
void list_init(struct list_node *head);

/* list_add inserts n after head. */
void list_add(struct list_node *head,
              struct list_node *n);

entry_t *registry_find(const registry_t *r, const char *name);
int registry_sort(registry_t *r, entry_cmp cmp);
static inline int list_empty(const struct list_node *head) { return head->next == head; }

#ifdef __cplusplus
}
#endif

#endif /* LIST_H */
