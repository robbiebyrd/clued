// References: calls, types, generics, inheritance and attributes.
using System;
using System.Collections.Generic;
using Demo.Data;

namespace Demo.Orders
{
    public interface IRepository<T> : IDisposable where T : Entity
    {
        T Find(Guid id);
    }

    public interface IOrderRepository : IRepository<Order>, IEnumerable<Order>
    {
    }

    [Table("orders")]
    public class Order : Entity, IComparable<Order>, Demo.Data.IAudited
    {
        public List<LineItem> Items { get; } = new();
        public Customer? Customer { get; set; }
        public Money[] Totals = Array.Empty<Money>();

        public int CompareTo(Order? other) => Id.CompareTo(other?.Id);
    }

    public sealed class OrderService : ServiceBase<Order>, IOrderService
    {
        private readonly IOrderRepository repository;
        private readonly ILogger<OrderService> logger;

        public OrderService(IOrderRepository repository, ILogger<OrderService> logger) : base(logger)
        {
            this.repository = repository;
            this.logger = logger;
        }

        [HttpGet]
        public Order Get(Guid id)
        {
            var order = repository.Find(id) ?? throw new NotFoundException(nameof(id));
            logger.LogInformation("found {Id}", order.Id);
            Validate(order);
            return order;
        }

        public decimal Total(Order order)
        {
            decimal sum = 0;
            foreach (LineItem item in order.Items)
            {
                if (item is DiscountedItem discounted)
                {
                    sum += discounted.Price.Apply(Discount.Default);
                    continue;
                }
                sum += ((Money)item.Price).Amount;
            }
            order.Customer?.Notify(new OrderTotalled(order, sum));
            return Math.Round(sum, 2, MidpointRounding.AwayFromZero);
        }

        private static void Validate<TEntity>(TEntity entity) where TEntity : Entity, IValidatable
        {
            try
            {
                entity.Validate();
            }
            catch (ValidationException e)
            {
                throw new InvalidOperationException(e.Message, e);
            }
            Console.WriteLine(typeof(TEntity).Name);
        }
    }

    public record OrderTotalled(Order Order, decimal Sum) : DomainEvent(Order.Id);

    public struct OrderId : IEquatable<OrderId>
    {
        public Guid Value;

        public bool Equals(OrderId other) => Value.Equals(other.Value);
    }
}
