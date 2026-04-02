/*
Copyright (C) 2025 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/

import React from 'react';
import { Avatar, Card, Empty, Skeleton, Typography } from '@douyinfe/semi-ui';
import { BarChart3, Flame } from 'lucide-react';
import { renderNumber, stringToColor } from '../../helpers';

const rankBadgeStyle = {
  width: 28,
  height: 28,
  borderRadius: 9999,
  display: 'inline-flex',
  alignItems: 'center',
  justifyContent: 'center',
  fontSize: 12,
  fontWeight: 700,
  background: 'var(--semi-color-fill-1)',
  color: 'var(--semi-color-text-0)',
  flexShrink: 0,
};

const leaderboardTitleStyle = {
  display: 'flex',
  alignItems: 'center',
  gap: 8,
  fontWeight: 600,
};

const getMetricText = (value, suffix = '') => {
  const formatted = renderNumber(value || 0);
  return suffix ? `${formatted} ${suffix}` : formatted;
};

const RankingList = ({
  CARD_PROPS,
  icon,
  title,
  description,
  metricKey,
  metricText,
  metricSuffix,
  data,
  loading,
  onShowUserInfo,
  t,
}) => {
  const items = Array.isArray(data) ? data.slice(0, 10) : [];

  return (
    <Card
      {...CARD_PROPS}
      title={
        <div className={leaderboardTitleStyle}>
          {icon}
          <span>{title}</span>
        </div>
      }
      bodyStyle={{ padding: 16 }}
      className='!rounded-2xl'
    >
      <Typography.Text type='tertiary' size='small'>
        {description}
      </Typography.Text>

      <div className='mt-4 space-y-3'>
        {loading &&
          Array.from({ length: 5 }).map((_, index) => (
            <div
              key={`skeleton-${index}`}
              className='flex items-center justify-between gap-3'
            >
              <div className='flex items-center gap-3 flex-1 min-w-0'>
                <Skeleton.Title
                  style={{ width: 28, height: 28, marginBottom: 0 }}
                />
                <Skeleton.Avatar style={{ width: 32, height: 32 }} />
                <Skeleton.Title
                  style={{ width: 120, height: 20, marginBottom: 0 }}
                />
              </div>
              <Skeleton.Title
                style={{ width: 72, height: 20, marginBottom: 0 }}
              />
            </div>
          ))}

        {!loading && items.length === 0 && (
          <Empty
            image={Empty.PRESENTED_IMAGE_SIMPLE}
            description={t('暂无排行数据')}
          />
        )}

        {!loading &&
          items.map((item, index) => (
            <div
              key={`${title}-${item.user_id}-${index}`}
              className='flex items-center justify-between gap-3 rounded-xl px-2 py-2 hover:bg-[var(--semi-color-fill-0)] transition-colors'
            >
              <div className='flex items-center gap-3 min-w-0 flex-1'>
                <span style={rankBadgeStyle}>{index + 1}</span>
                <Avatar
                  size='small'
                  color={stringToColor(item.username)}
                  style={{ cursor: 'pointer', flexShrink: 0 }}
                  onClick={(event) => {
                    event.stopPropagation();
                    onShowUserInfo?.(item.user_id);
                  }}
                >
                  {item.username?.slice(0, 1)?.toUpperCase()}
                </Avatar>
                <div className='min-w-0'>
                  <Typography.Text
                    ellipsis={{ showTooltip: true }}
                    style={{ display: 'block', fontWeight: 600 }}
                  >
                    {item.username}
                  </Typography.Text>
                  <Typography.Text type='tertiary' size='small'>
                    {metricText}
                  </Typography.Text>
                </div>
              </div>
              <Typography.Text strong>
                {getMetricText(item[metricKey], metricSuffix)}
              </Typography.Text>
            </div>
          ))}
      </div>
    </Card>
  );
};

const UserRankingsPanel = ({
  userRankings,
  userRankingsLoading,
  showUserInfoFunc,
  CARD_PROPS,
  t,
}) => {
  return (
    <div className='mb-4'>
      <div className='grid grid-cols-1 xl:grid-cols-2 gap-4'>
        <RankingList
          CARD_PROPS={CARD_PROPS}
          icon={<BarChart3 size={16} />}
          title={t('用户调用次数排行')}
          description={t('调用次数 Top 10，点击用户头像查看用户详情')}
          metricKey='call_count'
          metricText={t('调用次数')}
          data={userRankings?.request_count_ranking}
          loading={userRankingsLoading}
          onShowUserInfo={showUserInfoFunc}
          t={t}
        />
        <RankingList
          CARD_PROPS={CARD_PROPS}
          icon={<Flame size={16} />}
          title={t('用户消耗 Tokens 排行')}
          description={t('Tokens 消耗 Top 10，点击用户头像查看用户详情')}
          metricKey='token_used'
          metricText={t('消耗 Tokens')}
          metricSuffix='Tokens'
          data={userRankings?.token_used_ranking}
          loading={userRankingsLoading}
          onShowUserInfo={showUserInfoFunc}
          t={t}
        />
      </div>
    </div>
  );
};

export default UserRankingsPanel;
