#!/bin/bash
# 查看当前挂单价格梯度

echo "====== Phoenix 挂单价格检查 ======"
echo ""

# 提取最近的50个下单记录
tail -500 logs/phoenix_live.out | grep "下单成功" | tail -50 | while read line; do
    price=$(echo "$line" | grep -oP 'price=\K[0-9.]+')
    side=$(echo "$line" | grep -oP 'side=\K[A-Z]+')
    qty=$(echo "$line" | grep -oP 'qty=\K[0-9.]+')
    
    if [ ! -z "$price" ]; then
        echo "$side $price (qty: $qty)"
    fi
done | sort -k2 -n

echo ""
echo "====== 买卖价差分析 ======="


