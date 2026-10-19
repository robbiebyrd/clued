/** The store module: inventory and its services. */
@Deprecated(since = "2")
open module com.example.store {
    requires java.sql;
    requires transitive com.example.common;
    requires static lombok;
    exports com.example.store;
    exports com.example.store.internal to com.example.admin, com.example.tools;
    opens com.example.store.model;
    uses com.example.store.spi.StoreProvider;
    provides com.example.store.spi.StoreProvider with com.example.store.MemoryStoreProvider;
}
