/*
 * Copyright 2026 Example Corp.
 */
package com.example.store;

import java.util.ArrayList;
import java.util.List;
import java.util.Map;
import java.util.concurrent.*;
import static java.util.Objects.requireNonNull;
import static java.util.Collections.*;

/**
 * Inventory keeps the items of one warehouse.
 *
 * @param <T> the item type
 */
public class Inventory<T extends Item> extends AbstractStore implements Store<T>, AutoCloseable {

    /** The largest number of items a warehouse holds. */
    public static final int MAX_ITEMS = 10_000;

    private final List<T> items = new ArrayList<>();
    protected String name, region;
    int count;

    /** Creates an empty inventory called name. */
    public Inventory(String name) {
        this.name = requireNonNull(name);
    }

    /**
     * Adds an item.
     *
     * @return whether it was added
     */
    @Override
    public boolean add(T item) throws StoreException {
        if (items.size() >= MAX_ITEMS) {
            throw new StoreException("full: " + name);
        }
        count++;
        return items.add(item);
    }

    // find returns the first item with this id, or null.
    T find(String id) {
        for (T item : items) {
            if (item.id().equals(id)) {
                return item;
            }
        }
        return null;
    }

    private static <K, V> Map<K, V> index(List<V> values) {
        return emptyMap();
    }

    @Override
    public void close() {
        items.clear();
    }
}

/** Store is anything that holds items. */
interface Store<T> {
    int LIMIT = 5;

    boolean add(T item) throws StoreException;

    default int size() {
        return 0;
    }
}

/** Status of an item. */
public enum Status implements Labelled {
    /** On the shelf. */
    IN_STOCK("in stock"),
    SOLD_OUT("sold out");

    private final String label;

    Status(String label) {
        this.label = label;
    }

    @Override
    public String label() {
        return label;
    }
}

/** An item: id, price and status. */
public record Item(String id, long price, Status status) implements Comparable<Item> {
    public Item {
        requireNonNull(id);
    }

    public int compareTo(Item other) {
        return Long.compare(price, other.price);
    }
}

/** Marks a store method as audited. */
@interface Audited {
    String value() default "";
    int level();
}
