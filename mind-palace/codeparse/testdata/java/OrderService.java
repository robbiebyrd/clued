package com.example.orders;

import com.example.store.Inventory;
import com.example.store.Item;
import java.util.List;
import java.util.Optional;
import java.util.function.Function;
import java.util.stream.Collectors;

/** OrderService places orders against an inventory. */
@Service
@Transactional(readOnly = false)
public final class OrderService extends BaseService<Order> implements OrderApi, java.io.Serializable {

    private static final long serialVersionUID = 1L;

    private final Inventory<Item> inventory;
    private final java.util.concurrent.ExecutorService executor;
    private final Map.Entry<String, Order> last;

    public OrderService(Inventory<Item> inventory, Clock clock) {
        super(clock);
        this.inventory = inventory;
        this.executor = java.util.concurrent.Executors.newFixedThreadPool(4);
        this.last = null;
    }

    /** Places an order for the items, returning its id. */
    @Override
    public String place(List<Item> items) throws OrderException {
        var order = new Order(nextId(), items);
        items.forEach(inventory::add);
        List<String> ids = items.stream()
                .map(Item::id)
                .filter(id -> !id.isBlank())
                .collect(Collectors.toList());
        Function<Item, Long> price = item -> item.price();
        audit.record(order, Status.IN_STOCK);
        executor.submit(new Runnable() {
            @Override
            public void run() {
                notifyShipping(order);
            }
        });
        return super.place(order).id();
    }

    Optional<Order> find(String id) {
        return repository.findById(id).map(o -> new com.example.orders.Order(o));
    }

    private static String nextId() {
        return java.util.UUID.randomUUID().toString();
    }

    private void notifyShipping(Order order) {
        Shipping.Client client = new Shipping.Client();
        client.send(order);
    }
}

/** OrderApi is the public face of orders. */
public interface OrderApi extends Api, Comparable<OrderApi> {
    String place(List<Item> items) throws OrderException;
}
