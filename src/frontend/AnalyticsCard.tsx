import React from 'react';
import { useNavigate, Link } from 'react-router-dom';
import { FaLock, FaExclamationTriangle, FaArrowRight } from 'react-icons/fa';
import styles from './AnalyticsCard.module.scss';
import { formatDateTime } from '../../../utils/formatters';

interface MetricData {
  label: string;
  value: number | string;
}

interface AnalyticsCardProps {
  title: string;
  icon: React.ReactNode;
  metrics: MetricData[];
  route: string;
  progressValue?: number | null;
  showProgress?: boolean;
  isAuthorized?: boolean;
  isLoading?: boolean;
  error?: string | null;
  requiredRole?: string;
}

const AnalyticsCard: React.FC<AnalyticsCardProps> = ({ 
  title, 
  icon, 
  metrics = [], 
  route,
  progressValue = null,
  showProgress = false,
  isAuthorized = true,
  isLoading = false,
  error = null,
  requiredRole = null
}) => {
  const navigate = useNavigate();

  // Don't render anything if not authorized
  if (!isAuthorized) {
    return null;
  }

  // Ensure progress value is between 0 and 100
  const clampedProgress = Math.min(Math.max(progressValue || 0, 0), 100);

  const handleCardClick = () => {
    if (route && !isLoading && !error) {
      navigate(route);
    }
  };

  const handleKeyPress = (event: React.KeyboardEvent) => {
    if (event.key === 'Enter' || event.key === ' ') {
      event.preventDefault();
      handleCardClick();
    }
  };

  const formatMetricValue = (metric: MetricData): string | number => {
    if (metric.label === "Last Check-in" && typeof metric.value === 'string') {
      return metric.value === "None" ? "None" : formatDateTime(metric.value);
    }
    return metric.value;
  };

  // Error state
  if (error) {
    const isUnauthorized = error.includes('Unauthorized') || error.includes('not authorized');
    const isPlanGated = error.includes('not available on your current plan') || error.includes('not available');

    // Show upgrade prompt for plan-gated features instead of hiding
    if (isPlanGated) {
      return (
        <div className={`${styles.analyticsCard} ${styles.gatedCard}`}>
          <div className={styles.gatedContent}>
            <FaLock className={styles.gatedIcon} aria-hidden="true" />
            <h3 className={styles.gatedTitle}>Upgrade to unlock {title}</h3>
            <p className={styles.gatedMessage}>Available on Professional and Enterprise plans</p>
            <Link to="/pricing" className={styles.gatedLink}>
              View Plans <FaArrowRight />
            </Link>
          </div>
        </div>
      );
    }

    return (
      <div className={`${styles.analyticsCard} ${styles.errorCard}`}>
        <div className={styles.errorContent}>
          {isUnauthorized ? (
            <>
              <FaLock className={styles.errorIcon} aria-hidden="true" />
              <h3 className={styles.errorTitle}>Access Restricted</h3>
              <p className={styles.errorMessage}>
                {requiredRole ? `Requires ${requiredRole} access` : 'Insufficient permissions'}
              </p>
            </>
          ) : (
            <>
              <FaExclamationTriangle className={styles.errorIcon} aria-hidden="true" />
              <h3 className={styles.errorTitle}>Data Unavailable</h3>
              <p className={styles.errorMessage}>Unable to load analytics data</p>
            </>
          )}
        </div>
      </div>
    );
  }

  // Loading state
  if (isLoading) {
    return (
      <div className={`${styles.analyticsCard} ${styles.loadingCard}`}>
        <div className={styles.cardHeader}>
          <div className={styles.serviceIcon} aria-hidden="true">
            {icon}
          </div>
          <h3 className={styles.serviceTitle}>{title}</h3>
        </div>
        
        <div className={styles.analyticsMetrics}>
          {[1, 2].map((index) => (
            <div key={index} className={styles.analyticsMetric}>
              <div className={`${styles.metricValue} ${styles.skeleton}`} />
              <div className={`${styles.metricLabel} ${styles.skeleton}`} />
            </div>
          ))}
        </div>
        
        {showProgress && (
          <div className={styles.progressBar}>
            <div className={`${styles.progressFill} ${styles.skeleton}`} />
          </div>
        )}
      </div>
    );
  }

  // Normal state
  return (
    <div 
      className={styles.analyticsCard}
      onClick={handleCardClick}
      onKeyDown={handleKeyPress}
      role="button"
      tabIndex={0}
      aria-label={`Open ${title} analytics`}
    >
      <div className={styles.cardHeader}>
        <div className={styles.serviceIcon} aria-hidden="true">
          {icon}
        </div>
        <h3 className={styles.serviceTitle}>{title}</h3>
      </div>
      
      <div className={styles.analyticsMetrics}>
        {metrics.length > 0 ? (
          metrics.map((metric, index) => (
            <div key={index} className={styles.analyticsMetric}>
              <div className={styles.metricValue} aria-label={`${metric.label}: ${metric.value}`}>
                {formatMetricValue(metric)}
              </div>
              <div className={styles.metricLabel}>
                {metric.label}
              </div>
            </div>
          ))
        ) : (
          // Default metrics when no data available
          <>
            <div className={styles.analyticsMetric}>
              <div className={styles.metricValue}>0</div>
              <div className={styles.metricLabel}>No Data</div>
            </div>
            <div className={styles.analyticsMetric}>
              <div className={styles.metricValue}>0</div>
              <div className={styles.metricLabel}>Available</div>
            </div>
          </>
        )}
      </div>
      
      {showProgress && progressValue !== null && (
        <div className={styles.progressBar}>
          <div 
            className={styles.progressFill}
            style={{ width: `${clampedProgress}%` }}
            aria-valuenow={clampedProgress}
            aria-valuemin={0}
            aria-valuemax={100}
            aria-label={`Progress: ${clampedProgress}%`}
            role="progressbar"
          />
        </div>
      )}
    </div>
  );
};

export default AnalyticsCard;