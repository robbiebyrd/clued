package com.example.shop.service

import com.example.shop.model.Customer
import com.example.shop.model.Priced as PricedItem
import com.example.shop.model.*
import kotlinx.coroutines.flow.Flow
import java.io.Closeable

/** Stores customers. */
interface Repository<T> : Closeable, java.lang.AutoCloseable {
    suspend fun find(id: Long): T?
    fun all(): Flow<T>
}

abstract class BaseService(protected val name: String) {
    abstract fun start()
}

class CustomerService(
    private val repo: Repository<Customer>,
    private val clock: java.time.Clock,
) : BaseService("customers"), Repository<Customer> by repo, AutoCloseable {

    override fun start() {
        val now = clock.instant()
        log("started at $now")
        Catalog.price("sku-1")
    }

    suspend fun rename(id: Long, name: String): Customer? {
        val customer = repo.find(id) ?: return null
        customer.name = name
        val copy: Customer = customer.copy(name = name)
        return validate(copy)?.let { Customer.of(it.name) }
    }

    private fun validate(c: Customer): Customer? =
        if (c.name.length <= Customer.MAX_NAME) c else null

    override fun close() {
        super.close()
        repo.close()
    }
}

object Registry : BaseService("registry"), PricedItem {
    override val price: Long = 0
    override fun discounted(percent: Int): Long = price * (100 - percent) / 100
    override fun start() = Unit
}

fun log(message: String) {
    println(message)
}

fun services(): List<BaseService> = listOf(Registry, CustomerService(TODO(), java.time.Clock.systemUTC()))
